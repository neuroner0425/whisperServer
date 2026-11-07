package httptransport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/integrations/storage"
)

// mockGenericStorage implements storage.Storage but is NOT *storage.LocalStorage.
type mockGenericStorage struct{}

func (m *mockGenericStorage) Put(ctx context.Context, key string, data []byte, contentType string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{Key: key, Size: int64(len(data))}, nil
}
func (m *mockGenericStorage) PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{Key: key, Size: size}, nil
}
func (m *mockGenericStorage) Get(ctx context.Context, key string) ([]byte, error) {
	return []byte("generic"), nil
}
func (m *mockGenericStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	return io.NopCloser(bytes.NewReader([]byte("generic"))), storage.ObjectInfo{Key: key, Size: 7}, nil
}
func (m *mockGenericStorage) GetReadSeeker(ctx context.Context, key string) (storage.ReadSeekCloser, storage.ObjectInfo, error) {
	return nil, storage.ObjectInfo{}, nil
}
func (m *mockGenericStorage) Delete(ctx context.Context, key string) error {
	return nil
}
func (m *mockGenericStorage) DeletePrefix(ctx context.Context, prefix string) error {
	return nil
}
func (m *mockGenericStorage) Exists(ctx context.Context, key string) (bool, error) {
	return true, nil
}
func (m *mockGenericStorage) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{Key: key, Size: 7}, nil
}
func (m *mockGenericStorage) PresignPut(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	return "https://s3.example.com/put", nil
}
func (m *mockGenericStorage) PresignGet(ctx context.Context, key, downloadFilename string, expires time.Duration) (string, error) {
	return "https://s3.example.com/get", nil
}

// TestStress_S3AndNonLocalStorageBypass verifies that requests to /internal/local-blob/get and /put
// are strictly rejected with 404 Not Found when the storage backend is S3Storage, MemoryStorage,
// or any backend other than *storage.LocalStorage.
func TestStress_S3AndNonLocalStorageBypass(t *testing.T) {
	e := echo.New()

	futureExpires := strconv.FormatInt(time.Now().Add(1*time.Hour).Unix(), 10)
	dummySig := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	backends := []struct {
		name    string
		storage storage.Storage
	}{
		{
			name:    "MemoryStorage",
			storage: storage.NewMemoryStorage(),
		},
		{
			name:    "GenericMockStorage",
			storage: &mockGenericStorage{},
		},
		{
			name:    "S3StorageInstance",
			storage: &storage.S3Storage{},
		},
	}

	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			h := LocalBlobHandlers{Storage: tc.storage}

			// 1. GET attempt
			getURL := fmt.Sprintf("/internal/local-blob/get?key=jobs/job-1/audio_aac&expires=%s&sig=%s", futureExpires, dummySig)
			reqGet := httptest.NewRequest(http.MethodGet, getURL, nil)
			recGet := httptest.NewRecorder()
			cGet := e.NewContext(reqGet, recGet)

			errGet := h.Get()(cGet)
			if errGet == nil {
				t.Fatalf("[%s] expected 404 error on GET, got nil", tc.name)
			}
			heGet, ok := errGet.(*echo.HTTPError)
			if !ok || heGet.Code != http.StatusNotFound {
				t.Fatalf("[%s] expected HTTP 404 Not Found on GET, got %v", tc.name, errGet)
			}
			if !strings.Contains(heGet.Message.(string), "Local blob endpoint unavailable on current storage backend") {
				t.Fatalf("[%s] expected specific bypass rejection message on GET, got %v", tc.name, heGet.Message)
			}

			// 2. PUT attempt
			putURL := fmt.Sprintf("/internal/local-blob/put?key=jobs/job-1/quarantine_raw&expires=%s&sig=%s", futureExpires, dummySig)
			reqPut := httptest.NewRequest(http.MethodPut, putURL, bytes.NewReader([]byte("malicious-upload-payload")))
			recPut := httptest.NewRecorder()
			cPut := e.NewContext(reqPut, recPut)

			errPut := h.Put()(cPut)
			if errPut == nil {
				t.Fatalf("[%s] expected 404 error on PUT, got nil", tc.name)
			}
			hePut, ok := errPut.(*echo.HTTPError)
			if !ok || hePut.Code != http.StatusNotFound {
				t.Fatalf("[%s] expected HTTP 404 Not Found on PUT, got %v", tc.name, errPut)
			}
			if !strings.Contains(hePut.Message.(string), "Local blob endpoint unavailable on current storage backend") {
				t.Fatalf("[%s] expected specific bypass rejection message on PUT, got %v", tc.name, hePut.Message)
			}
		})
	}

	// 3. Storage == nil -> 503 Service Unavailable
	t.Run("NilStorageReturns503", func(t *testing.T) {
		hNil := LocalBlobHandlers{Storage: nil}

		reqGet := httptest.NewRequest(http.MethodGet, "/internal/local-blob/get?key=abc&expires=123&sig=abc", nil)
		recGet := httptest.NewRecorder()
		cGet := e.NewContext(reqGet, recGet)
		errGet := hNil.Get()(cGet)
		if he, ok := errGet.(*echo.HTTPError); !ok || he.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 Service Unavailable on nil storage GET, got %v", errGet)
		}

		reqPut := httptest.NewRequest(http.MethodPut, "/internal/local-blob/put?key=abc&expires=123&sig=abc", nil)
		recPut := httptest.NewRecorder()
		cPut := e.NewContext(reqPut, recPut)
		errPut := hNil.Put()(cPut)
		if he, ok := errPut.(*echo.HTTPError); !ok || he.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 Service Unavailable on nil storage PUT, got %v", errPut)
		}
	})
}

// TestStress_PathTraversal_HTTPHandlers tests directory traversal attack strings against
// the /internal/local-blob/get and /put HTTP handlers.
func TestStress_PathTraversal_HTTPHandlers(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("stress-secret-key-path-traversal"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	h := LocalBlobHandlers{
		Storage: store,
	}
	e := echo.New()
	futureExpires := strconv.FormatInt(time.Now().Add(1*time.Hour).Unix(), 10)

	// URL query key raw parameters as sent by HTTP clients over the wire
	traversalQueryStrings := []string{
		"../",
		"..%2F",
		"/../../",
		"jobs/../../etc/passwd",
		"....//",
		"..",
		"....",
		"jobs/../secret.txt",
		"jobs/..\\..\\etc\\passwd",
		"jobs/....//....//etc/passwd",
		"%2e%2e%2f",
		".%2e/",
		"jobs/%2e%2e/passwd",
		"jobs/..%2f..%2fetc/passwd",
	}

	for _, rawQueryKey := range traversalQueryStrings {
		t.Run("RawQuery:"+rawQueryKey, func(t *testing.T) {
			// Construct URL with rawQueryKey directly in key query parameter
			getURL := fmt.Sprintf("/internal/local-blob/get?key=%s&expires=%s&sig=dummysig", rawQueryKey, futureExpires)
			reqGet := httptest.NewRequest(http.MethodGet, getURL, nil)
			recGet := httptest.NewRecorder()
			cGet := e.NewContext(reqGet, recGet)

			errGet := h.Get()(cGet)
			if errGet == nil {
				t.Fatalf("expected error on traversal query %q for GET, got nil", rawQueryKey)
			}
			heGet, ok := errGet.(*echo.HTTPError)
			if !ok {
				t.Fatalf("expected echo.HTTPError on %q, got %v", rawQueryKey, errGet)
			}
			// Both 400 (Bad Request - traversal detected) and 403 (Forbidden - invalid sig/tampered) are safe rejections
			if heGet.Code != http.StatusBadRequest && heGet.Code != http.StatusForbidden {
				t.Fatalf("expected HTTP 400 or 403 on traversal query %q for GET, got %d (%v)", rawQueryKey, heGet.Code, heGet.Message)
			}

			// For the 5 explicit prompt vectors: ../, ..%2F, /../../, jobs/../../etc/passwd, ....//
			// they must be rejected with 400 Bad Request
			canonicalFive := map[string]bool{
				"../":                   true,
				"..%2F":                 true,
				"/../../":               true,
				"jobs/../../etc/passwd": true,
				"....//":                true,
				"%2e%2e%2f":             true, // URL decoded to ../
				".%2e/":                 true, // URL decoded to ../
				"jobs/%2e%2e/passwd":    true, // URL decoded to jobs/../passwd
				"jobs/..%2f..%2fetc/passwd": true,
			}
			if canonicalFive[rawQueryKey] {
				if heGet.Code != http.StatusBadRequest {
					t.Fatalf("expected HTTP 400 Bad Request on canonical traversal vector %q for GET, got %d (%v)", rawQueryKey, heGet.Code, heGet.Message)
				}
				if !strings.Contains(heGet.Message.(string), "directory traversal detected") {
					t.Fatalf("expected 'directory traversal detected' for %q, got %v", rawQueryKey, heGet.Message)
				}
			}

			// Test PUT
			putURL := fmt.Sprintf("/internal/local-blob/put?key=%s&expires=%s&sig=dummysig", rawQueryKey, futureExpires)
			reqPut := httptest.NewRequest(http.MethodPut, putURL, bytes.NewReader([]byte("traversal-payload")))
			recPut := httptest.NewRecorder()
			cPut := e.NewContext(reqPut, recPut)

			errPut := h.Put()(cPut)
			if errPut == nil {
				t.Fatalf("expected error on traversal query %q for PUT, got nil", rawQueryKey)
			}
			hePut, ok := errPut.(*echo.HTTPError)
			if !ok {
				t.Fatalf("expected echo.HTTPError on %q, got %v", rawQueryKey, errPut)
			}
			if hePut.Code != http.StatusBadRequest && hePut.Code != http.StatusForbidden {
				t.Fatalf("expected HTTP 400 or 403 on traversal query %q for PUT, got %d (%v)", rawQueryKey, hePut.Code, hePut.Message)
			}
			if canonicalFive[rawQueryKey] {
				if hePut.Code != http.StatusBadRequest {
					t.Fatalf("expected HTTP 400 Bad Request on canonical traversal vector %q for PUT, got %d (%v)", rawQueryKey, hePut.Code, hePut.Message)
				}
				if !strings.Contains(hePut.Message.(string), "directory traversal detected") {
					t.Fatalf("expected 'directory traversal detected' for %q, got %v", rawQueryKey, hePut.Message)
				}
			}
		})
	}
}
