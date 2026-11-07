package httptransport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/integrations/storage"
)

func TestLocalBlobHandlers_PutAndGet(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("test-secret-key"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	h := LocalBlobHandlers{
		Storage: store,
	}

	e := echo.New()

	// 1. Generate PresignPut URL
	putURL, err := store.PresignPut(nil, "jobs/job-1/quarantine_raw", "audio/mp4", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut failed: %v", err)
	}

	// Test PUT
	reqBody := []byte("audio-sample-binary-content-1234567890")
	req := httptest.NewRequest(http.MethodPut, putURL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "audio/mp4")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = h.Put()(c)
	if err != nil {
		t.Fatalf("Put() returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 2. Generate PresignGet URL
	getURL, err := store.PresignGet(nil, "jobs/job-1/quarantine_raw", "my_audio.mp4", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet failed: %v", err)
	}

	// Test GET
	req = httptest.NewRequest(http.MethodGet, getURL, nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)

	err = h.Get()(c)
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), reqBody) {
		t.Fatalf("expected body %q, got %q", string(reqBody), rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "my_audio.mp4") {
		t.Fatalf("expected Content-Disposition header with filename, got %s", rec.Header().Get("Content-Disposition"))
	}

	// 3. Test Range Request
	req = httptest.NewRequest(http.MethodGet, getURL, nil)
	req.Header.Set("Range", "bytes=0-4")
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)

	err = h.Get()(c)
	if err != nil {
		t.Fatalf("Get() range returned error: %v", err)
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d", rec.Code)
	}
	if rec.Body.String() != "audio" {
		t.Fatalf("expected 'audio', got %q", rec.Body.String())
	}

	// 4. Test Tampered Signature
	tamperedURL := putURL + "tampered"
	req = httptest.NewRequest(http.MethodPut, tamperedURL, bytes.NewReader([]byte("data")))
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Put()(c)
	if err == nil {
		t.Fatalf("expected error for tampered signature, got nil")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// 5. Test Expired URL (replace expires timestamp with past unix timestamp)
	u, err := url.Parse(putURL)
	if err != nil {
		t.Fatalf("url.Parse failed: %v", err)
	}
	q := u.Query()
	q.Set("expires", "1000") // year 1970
	u.RawQuery = q.Encode()
	expiredURL := u.String()

	req = httptest.NewRequest(http.MethodPut, expiredURL, io.NopCloser(bytes.NewReader([]byte("data"))))
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Put()(c)
	if err == nil {
		t.Fatalf("expected error for expired url, got nil")
	}
	he, ok = err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// 6. Test Non-LocalStorage Backend HMAC Bypass Prevention (SEC-01)
	nonLsHandler := LocalBlobHandlers{
		Storage: &mockNonLocalStorageStorage{},
	}
	// Try GET on non-LocalStorage
	reqNonLsGet := httptest.NewRequest(http.MethodGet, "/internal/local-blob/get?key=test.mp3&expires=9999999999&sig=dummy", nil)
	recNonLsGet := httptest.NewRecorder()
	cNonLsGet := e.NewContext(reqNonLsGet, recNonLsGet)
	err = nonLsHandler.Get()(cNonLsGet)
	if err == nil {
		t.Fatal("expected 404 Not Found when storage is not LocalStorage, got nil")
	}
	heNonLsGet, ok := err.(*echo.HTTPError)
	if !ok || heNonLsGet.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got: %v", err)
	}

	// Try PUT on non-LocalStorage
	reqNonLsPut := httptest.NewRequest(http.MethodPut, "/internal/local-blob/put?key=test.mp3&expires=9999999999&sig=dummy", bytes.NewReader([]byte("data")))
	recNonLsPut := httptest.NewRecorder()
	cNonLsPut := e.NewContext(reqNonLsPut, recNonLsPut)
	err = nonLsHandler.Put()(cNonLsPut)
	if err == nil {
		t.Fatal("expected 404 Not Found when storage is not LocalStorage, got nil")
	}
	heNonLsPut, ok := err.(*echo.HTTPError)
	if !ok || heNonLsPut.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got: %v", err)
	}

	// 7. Test Directory Traversal Rejection in LocalBlobHandlers
	reqTrav := httptest.NewRequest(http.MethodGet, "/internal/local-blob/get?key=../../etc/passwd&expires=9999999999&sig=dummy", nil)
	recTrav := httptest.NewRecorder()
	cTrav := e.NewContext(reqTrav, recTrav)
	err = h.Get()(cTrav)
	if err == nil {
		t.Fatal("expected 400 Bad Request for traversal key, got nil")
	}
	heTrav, ok := err.(*echo.HTTPError)
	if !ok || heTrav.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got: %v", err)
	}
}

type mockNonLocalStorageStorage struct{}

func (m *mockNonLocalStorageStorage) Put(ctx context.Context, key string, data []byte, contentType string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, nil
}
func (m *mockNonLocalStorageStorage) PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, nil
}
func (m *mockNonLocalStorageStorage) Get(ctx context.Context, key string) ([]byte, error) {
	return nil, nil
}
func (m *mockNonLocalStorageStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	return io.NopCloser(bytes.NewReader(nil)), storage.ObjectInfo{}, nil
}
func (m *mockNonLocalStorageStorage) GetReadSeeker(ctx context.Context, key string) (storage.ReadSeekCloser, storage.ObjectInfo, error) {
	return nil, storage.ObjectInfo{}, nil
}
func (m *mockNonLocalStorageStorage) Delete(ctx context.Context, key string) error {
	return nil
}
func (m *mockNonLocalStorageStorage) DeletePrefix(ctx context.Context, prefix string) error {
	return nil
}
func (m *mockNonLocalStorageStorage) Exists(ctx context.Context, key string) (bool, error) {
	return false, nil
}
func (m *mockNonLocalStorageStorage) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, nil
}
func (m *mockNonLocalStorageStorage) PresignPut(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	return "https://s3.example.com/put", nil
}
func (m *mockNonLocalStorageStorage) PresignGet(ctx context.Context, key, downloadFilename string, expires time.Duration) (string, error) {
	return "https://s3.example.com/get", nil
}
