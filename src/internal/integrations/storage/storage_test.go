package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"whisperserver/src/internal/integrations/storage"
)

func runCommonStorageTests(t *testing.T, s storage.Storage) {
	ctx := context.Background()

	// 1. Initially object should not exist
	exists, err := s.Exists(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("exists error: %v", err)
	}
	if exists {
		t.Fatalf("expected object to not exist")
	}

	_, err = s.Get(ctx, "jobs/123/audio.m4a")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// 2. Put object
	data := []byte("fake-audio-binary-data")
	info, err := s.Put(ctx, "jobs/123/audio.m4a", data, "audio/mp4")
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}
	if info.Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), info.Size)
	}

	// 3. Exists & Stat
	exists, err = s.Exists(ctx, "jobs/123/audio.m4a")
	if err != nil || !exists {
		t.Fatalf("expected object to exist, got exists=%v err=%v", exists, err)
	}

	stat, err := s.Stat(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if stat.Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), stat.Size)
	}

	// 4. Get
	fetched, err := s.Get(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if string(fetched) != string(data) {
		t.Fatalf("got data mismatch: %s != %s", fetched, data)
	}

	// 5. Stream Get
	rc, rinfo, err := s.GetStream(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("get stream failed: %v", err)
	}
	defer rc.Close()
	if rinfo.Size != int64(len(data)) {
		t.Fatalf("stream info size mismatch")
	}

	// 5-1. ReadSeeker Get
	rsc, rsinfo, err := s.GetReadSeeker(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("get read seeker failed: %v", err)
	}
	defer rsc.Close()
	if rsinfo.Size != int64(len(data)) {
		t.Fatalf("read seeker info size mismatch")
	}
	// Test Seek offset
	offset, err := rsc.Seek(5, 0)
	if err != nil || offset != 5 {
		t.Fatalf("seek failed: offset=%d err=%v", offset, err)
	}
	buf := make([]byte, 4)
	n, err := rsc.Read(buf)
	if err != nil || n != 4 || string(buf) != "audi" {
		t.Fatalf("read after seek failed: n=%d buf=%q err=%v", n, string(buf), err)
	}

	// 6. Delete
	if err := s.Delete(ctx, "jobs/123/audio.m4a"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	exists, err = s.Exists(ctx, "jobs/123/audio.m4a")
	if err != nil || exists {
		t.Fatalf("expected object deleted, exists=%v", exists)
	}

	// 7. DeletePrefix
	_, _ = s.Put(ctx, "jobs/abc/file1", []byte("1"), "text/plain")
	_, _ = s.Put(ctx, "jobs/abc/file2", []byte("2"), "text/plain")
	_, _ = s.Put(ctx, "jobs/def/file3", []byte("3"), "text/plain")

	if err := s.DeletePrefix(ctx, "jobs/abc/"); err != nil {
		t.Fatalf("delete prefix failed: %v", err)
	}
	ex1, _ := s.Exists(ctx, "jobs/abc/file1")
	ex2, _ := s.Exists(ctx, "jobs/abc/file2")
	ex3, _ := s.Exists(ctx, "jobs/def/file3")
	if ex1 || ex2 {
		t.Fatalf("expected abc files deleted")
	}
	if !ex3 {
		t.Fatalf("expected def file preserved")
	}

	// 8. Presign Put & Get basic contract
	putURL, err := s.PresignPut(ctx, "jobs/123/presign.bin", "application/octet-stream", 15*time.Minute)
	if err != nil {
		t.Fatalf("presign put failed: %v", err)
	}
	if putURL == "" {
		t.Fatalf("expected non-empty put url")
	}

	getURL, err := s.PresignGet(ctx, "jobs/123/presign.bin", "download.bin", 15*time.Minute)
	if err != nil {
		t.Fatalf("presign get failed: %v", err)
	}
	if getURL == "" {
		t.Fatalf("expected non-empty get url")
	}
}

func TestMemoryStorage(t *testing.T) {
	s := storage.NewMemoryStorage()
	runCommonStorageTests(t, s)
}

func TestLocalStorage(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "storage")
	defer os.RemoveAll(tmp)

	s, err := storage.NewLocalStorage(tmp)
	if err != nil {
		t.Fatalf("init local storage: %v", err)
	}
	runCommonStorageTests(t, s)
}

func TestLocalStorage_PathTraversalRejection(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "storage")
	defer os.RemoveAll(tmp)

	s, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("test-secret"))
	if err != nil {
		t.Fatalf("init local storage: %v", err)
	}
	ctx := context.Background()

	traversalKeys := []string{
		"../../etc/passwd",
		"../../../secret.txt",
		"jobs/../../escape.bin",
		"/../outside",
		`..\windows\path`,
		`jobs\..\escape`,
	}

	for _, key := range traversalKeys {
		// 1. Put
		if _, err := s.Put(ctx, key, []byte("evil"), "text/plain"); err == nil {
			t.Errorf("expected Put to fail for traversal key %q, got nil", key)
		}

		// 2. Get
		if _, err := s.Get(ctx, key); err == nil {
			t.Errorf("expected Get to fail for traversal key %q, got nil", key)
		}

		// 3. GetStream
		if _, _, err := s.GetStream(ctx, key); err == nil {
			t.Errorf("expected GetStream to fail for traversal key %q, got nil", key)
		}

		// 4. GetReadSeeker
		if _, _, err := s.GetReadSeeker(ctx, key); err == nil {
			t.Errorf("expected GetReadSeeker to fail for traversal key %q, got nil", key)
		}

		// 5. Delete
		if err := s.Delete(ctx, key); err == nil {
			t.Errorf("expected Delete to fail for traversal key %q, got nil", key)
		}

		// 6. DeletePrefix
		if err := s.DeletePrefix(ctx, key); err == nil {
			t.Errorf("expected DeletePrefix to fail for traversal key %q, got nil", key)
		}

		// 7. Exists
		if _, err := s.Exists(ctx, key); err == nil {
			t.Errorf("expected Exists to fail for traversal key %q, got nil", key)
		}

		// 8. Stat
		if _, err := s.Stat(ctx, key); err == nil {
			t.Errorf("expected Stat to fail for traversal key %q, got nil", key)
		}

		// 9. PresignPut
		if _, err := s.PresignPut(ctx, key, "text/plain", 15*time.Minute); err == nil {
			t.Errorf("expected PresignPut to fail for traversal key %q, got nil", key)
		}

		// 10. PresignGet
		if _, err := s.PresignGet(ctx, key, "evil.txt", 15*time.Minute); err == nil {
			t.Errorf("expected PresignGet to fail for traversal key %q, got nil", key)
		}

		// 11. VerifyLocalToken
		if s.VerifyLocalToken("get", key, "", time.Now().Add(10*time.Minute).Unix(), "dummy-sig") {
			t.Errorf("expected VerifyLocalToken to return false for traversal key %q", key)
		}
	}
}

func TestS3StorageLocalMinIO(t *testing.T) {
	// Test against local MinIO with user-provided credentials (test / test1234!)
	cfg := storage.S3Config{
		Endpoint:        "localhost:9000",
		Region:          "us-east-1",
		Bucket:          "whisper-blobs-test",
		AccessKeyID:     "test",
		SecretAccessKey: "test1234!",
		UseSSL:          false,
	}

	s, err := storage.NewS3Storage(context.Background(), cfg)
	if err != nil {
		t.Skipf("skipping MinIO integration test: %v", err)
		return
	}

	runCommonStorageTests(t, s)
}

func TestLocalStoragePresignAndVerify(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	s, err := storage.NewLocalStorage(tmp,
		storage.WithEndpointBase("http://localhost:8080/internal/local-blob"),
		storage.WithSecretKey("custom-test-secret"),
	)
	if err != nil {
		t.Fatalf("failed to init local storage: %v", err)
	}

	// 1. PresignPut
	key := "jobs/456/audio.wav"
	putURL, err := s.PresignPut(ctx, key, "audio/wav", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut failed: %v", err)
	}
	if !strings.HasPrefix(putURL, "http://localhost:8080/internal/local-blob/put?") {
		t.Fatalf("expected putURL to start with endpoint/put?, got: %s", putURL)
	}

	u, err := url.Parse(putURL)
	if err != nil {
		t.Fatalf("failed to parse putURL: %v", err)
	}
	putQuery := u.Query()
	extractedKey := putQuery.Get("key")
	if extractedKey != key {
		t.Fatalf("expected key %q, got %q", key, extractedKey)
	}
	putExpiresStr := putQuery.Get("expires")
	putExpiresUnix, err := strconv.ParseInt(putExpiresStr, 10, 64)
	if err != nil {
		t.Fatalf("invalid expires in putURL: %v", err)
	}
	putSig := putQuery.Get("sig")
	if putSig == "" {
		t.Fatalf("expected non-empty sig in putURL")
	}

	// Verify valid put token
	if !s.VerifyLocalToken("put", extractedKey, "", putExpiresUnix, putSig) {
		t.Fatalf("expected put token to be valid")
	}

	// 2. PresignGet with downloadFilename
	getURL, err := s.PresignGet(ctx, key, "my-audio.wav", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet failed: %v", err)
	}
	if !strings.HasPrefix(getURL, "http://localhost:8080/internal/local-blob/get?") {
		t.Fatalf("expected getURL to start with endpoint/get?, got: %s", getURL)
	}

	ug, err := url.Parse(getURL)
	if err != nil {
		t.Fatalf("failed to parse getURL: %v", err)
	}
	getQuery := ug.Query()
	if getQuery.Get("key") != key {
		t.Fatalf("expected key %q, got %q", key, getQuery.Get("key"))
	}
	if getQuery.Get("filename") != "my-audio.wav" {
		t.Fatalf("expected filename 'my-audio.wav', got %q", getQuery.Get("filename"))
	}
	getExpiresUnix, err := strconv.ParseInt(getQuery.Get("expires"), 10, 64)
	if err != nil {
		t.Fatalf("invalid expires in getURL: %v", err)
	}
	getSig := getQuery.Get("sig")
	if getSig == "" {
		t.Fatalf("expected non-empty sig in getURL")
	}

	// Verify valid get token
	if !s.VerifyLocalToken("get", key, "my-audio.wav", getExpiresUnix, getSig) {
		t.Fatalf("expected get token to be valid")
	}

	// 3. PresignGet without downloadFilename
	getURLNoName, err := s.PresignGet(ctx, key, "", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet without filename failed: %v", err)
	}
	ugNoName, err := url.Parse(getURLNoName)
	if err != nil {
		t.Fatalf("failed to parse getURLNoName: %v", err)
	}
	if ugNoName.Query().Get("filename") != "" {
		t.Fatalf("expected no filename in getURLNoName")
	}
	noNameExpires, _ := strconv.ParseInt(ugNoName.Query().Get("expires"), 10, 64)
	noNameSig := ugNoName.Query().Get("sig")
	if !s.VerifyLocalToken("get", key, "", noNameExpires, noNameSig) {
		t.Fatalf("expected get token without filename to be valid")
	}

	// 4. Verification failure cases (tampering, expiration, wrong keys)
	tests := []struct {
		name     string
		action   string
		key      string
		filename string
		expires  int64
		sig      string
		want     bool
	}{
		{
			name:     "tampered key",
			action:   "put",
			key:      "tampered/key",
			filename: "",
			expires:  putExpiresUnix,
			sig:      putSig,
			want:     false,
		},
		{
			name:     "tampered action (get with put sig)",
			action:   "get",
			key:      key,
			filename: "",
			expires:  putExpiresUnix,
			sig:      putSig,
			want:     false,
		},
		{
			name:     "tampered filename",
			action:   "get",
			key:      key,
			filename: "malicious.exe",
			expires:  getExpiresUnix,
			sig:      getSig,
			want:     false,
		},
		{
			name:     "expired token",
			action:   "put",
			key:      key,
			filename: "",
			expires:  time.Now().Add(-10 * time.Second).Unix(),
			sig:      putSig,
			want:     false,
		},
		{
			name:     "tampered signature",
			action:   "put",
			key:      key,
			filename: "",
			expires:  putExpiresUnix,
			sig:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want:     false,
		},
		{
			name:     "empty signature",
			action:   "put",
			key:      key,
			filename: "",
			expires:  putExpiresUnix,
			sig:      "",
			want:     false,
		},
		{
			name:     "empty key",
			action:   "put",
			key:      "",
			filename: "",
			expires:  putExpiresUnix,
			sig:      putSig,
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := s.VerifyLocalToken(tc.action, tc.key, tc.filename, tc.expires, tc.sig)
			if got != tc.want {
				t.Errorf("VerifyLocalToken(%s, %s, %s) = %v; want %v", tc.action, tc.key, tc.filename, got, tc.want)
			}
		})
	}

	// 5. Verification with different secret must fail
	otherStore, err := storage.NewLocalStorage(t.TempDir(), storage.WithSecretKey("completely-different-key"))
	if err != nil {
		t.Fatalf("failed to init other store: %v", err)
	}
	if otherStore.VerifyLocalToken("put", key, "", putExpiresUnix, putSig) {
		t.Fatalf("expected token verification to fail with different secret")
	}

	// 6. Input validation errors
	if _, err := s.PresignPut(ctx, "", "text/plain", time.Minute); err == nil {
		t.Errorf("expected error for empty key in PresignPut")
	}
	if _, err := s.PresignPut(ctx, "k", "text/plain", 0); err == nil {
		t.Errorf("expected error for zero expires in PresignPut")
	}
	if _, err := s.PresignGet(ctx, "", "file.txt", time.Minute); err == nil {
		t.Errorf("expected error for empty key in PresignGet")
	}
	if _, err := s.PresignGet(ctx, "k", "file.txt", -time.Minute); err == nil {
		t.Errorf("expected error for negative expires in PresignGet")
	}
}

func TestMemoryStoragePresign(t *testing.T) {
	ctx := context.Background()
	s := storage.NewMemoryStorage()

	putURL, err := s.PresignPut(ctx, "mock/upload.m4a", "audio/mp4", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut failed: %v", err)
	}
	if putURL != "memory://put/mock/upload.m4a" {
		t.Fatalf("unexpected put URL: %s", putURL)
	}

	getURL, err := s.PresignGet(ctx, "mock/upload.m4a", "download.m4a", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet failed: %v", err)
	}
	if getURL != "memory://get/mock/upload.m4a?filename=download.m4a" {
		t.Fatalf("unexpected get URL: %s", getURL)
	}

	getURLNoName, err := s.PresignGet(ctx, "mock/upload.m4a", "", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet without filename failed: %v", err)
	}
	if getURLNoName != "memory://get/mock/upload.m4a" {
		t.Fatalf("unexpected get URL without filename: %s", getURLNoName)
	}

	// Validation
	if _, err := s.PresignPut(ctx, "", "audio/mp4", time.Minute); err == nil {
		t.Errorf("expected error for empty key in PresignPut")
	}
	if _, err := s.PresignGet(ctx, "", "download.m4a", time.Minute); err == nil {
		t.Errorf("expected error for empty key in PresignGet")
	}
}

func TestS3StoragePresign(t *testing.T) {
	cfg := storage.S3Config{
		Endpoint:        "localhost:9000",
		Region:          "us-east-1",
		Bucket:          "whisper-blobs-test",
		AccessKeyID:     "test",
		SecretAccessKey: "test1234!",
		UseSSL:          false,
	}

	ctx := context.Background()
	s, err := storage.NewS3Storage(ctx, cfg)
	if err != nil {
		t.Skipf("skipping MinIO presign integration test: %v", err)
		return
	}

	key := "test-presign/hello.txt"
	content := []byte("hello world presigned s3 upload")

	// 1. PresignPut
	putURL, err := s.PresignPut(ctx, key, "text/plain", 15*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut failed: %v", err)
	}
	if !strings.Contains(putURL, "whisper-blobs-test") || !strings.Contains(putURL, "X-Amz-Signature") {
		t.Fatalf("PresignPut URL does not look like valid S3 presigned URL: %s", putURL)
	}

	// Direct HTTP PUT using presigned URL
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("failed to create PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to execute PUT using presigned URL: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from presigned PUT, got %d", resp.StatusCode)
	}

	// 2. PresignGet with downloadFilename
	getURL, err := s.PresignGet(ctx, key, "custom_hello.txt", 15*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet failed: %v", err)
	}
	if !strings.Contains(getURL, "response-content-disposition") {
		t.Fatalf("expected response-content-disposition in getURL: %s", getURL)
	}

	// Direct HTTP GET using presigned URL
	getResp, err := http.Get(getURL)
	if err != nil {
		t.Fatalf("failed to execute GET using presigned URL: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK from presigned GET, got %d", getResp.StatusCode)
	}
	body, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != string(content) {
		t.Fatalf("body mismatch: got %q, want %q", string(body), string(content))
	}
	disp := getResp.Header.Get("Content-Disposition")
	if !strings.Contains(disp, "custom_hello.txt") {
		t.Fatalf("expected Content-Disposition with filename, got: %s", disp)
	}

	// 3. PresignGet without downloadFilename
	getURLNoName, err := s.PresignGet(ctx, key, "", 15*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet without filename failed: %v", err)
	}
	if strings.Contains(getURLNoName, "response-content-disposition") {
		t.Fatalf("did not expect response-content-disposition in getURLNoName: %s", getURLNoName)
	}

	// Validation
	if _, err := s.PresignPut(ctx, "", "text/plain", time.Minute); err == nil {
		t.Errorf("expected error for empty key in PresignPut")
	}
	if _, err := s.PresignPut(ctx, "k", "text/plain", 0); err == nil {
		t.Errorf("expected error for zero expires in PresignPut")
	}
	if _, err := s.PresignGet(ctx, "", "out.txt", time.Minute); err == nil {
		t.Errorf("expected error for empty key in PresignGet")
	}
	if _, err := s.PresignGet(ctx, "k", "out.txt", -time.Minute); err == nil {
		t.Errorf("expected error for negative expires in PresignGet")
	}
}
