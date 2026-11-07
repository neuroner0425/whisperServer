package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultEndpointBase = "/internal/local-blob"
	defaultSecretKey    = "whisper-local-storage-secret"
)

// LocalStorage implements Storage on top of a local filesystem directory.
type LocalStorage struct {
	baseDir      string
	secretKey    []byte
	endpointBase string
}

// LocalStorageOption configures optional settings for LocalStorage.
type LocalStorageOption func(*LocalStorage)

// WithSecretKey configures the HMAC signing key for local presigned URLs.
func WithSecretKey(secret string) LocalStorageOption {
	return func(s *LocalStorage) {
		if strings.TrimSpace(secret) != "" {
			s.secretKey = []byte(secret)
		}
	}
}

// WithEndpointBase configures the base URL/path for local presigned URLs.
func WithEndpointBase(endpoint string) LocalStorageOption {
	return func(s *LocalStorage) {
		s.endpointBase = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	}
}

// NewLocalStorage instantiates a LocalStorage root in the given directory.
func NewLocalStorage(baseDir string, opts ...LocalStorageOption) (*LocalStorage, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("local_storage: base directory must not be empty")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("local_storage: create base dir %q failed: %w", baseDir, err)
	}

	secret := os.Getenv("LOCAL_STORAGE_SECRET")
	if strings.TrimSpace(secret) == "" {
		secret = defaultSecretKey
	}

	endpoint := os.Getenv("LOCAL_STORAGE_ENDPOINT")
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultEndpointBase
	}

	s := &LocalStorage{
		baseDir:      baseDir,
		secretKey:    []byte(secret),
		endpointBase: strings.TrimRight(strings.TrimSpace(endpoint), "/"),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s, nil
}

// SetSecretKey updates the secret key used for HMAC signing.
func (s *LocalStorage) SetSecretKey(secret string) {
	if strings.TrimSpace(secret) != "" {
		s.secretKey = []byte(secret)
	}
}

// SetEndpointBase updates the endpoint base for presigned URLs.
func (s *LocalStorage) SetEndpointBase(endpoint string) {
	s.endpointBase = strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

func (s *LocalStorage) pathFor(key string) (string, error) {
	if s == nil || s.baseDir == "" {
		return "", errors.New("local_storage: storage is not initialized")
	}
	if strings.Contains(key, "..") {
		return "", fmt.Errorf("local_storage: invalid key traversal: %q", key)
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	clean = strings.TrimPrefix(clean, "/")
	clean = strings.TrimPrefix(clean, "\\")
	target := filepath.Join(s.baseDir, clean)
	rel, err := filepath.Rel(s.baseDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", fmt.Errorf("local_storage: path escapes base dir: %q", key)
	}
	return target, nil
}

// Put writes data into the local filesystem.
func (s *LocalStorage) Put(ctx context.Context, key string, data []byte, contentType string) (ObjectInfo, error) {
	return s.PutStream(ctx, key, bytes.NewReader(data), int64(len(data)), contentType)
}

// PutStream streams an io.Reader to a local file.
func (s *LocalStorage) PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (ObjectInfo, error) {
	if s == nil || s.baseDir == "" {
		return ObjectInfo{}, errors.New("local_storage: storage is not initialized")
	}
	targetPath, err := s.pathFor(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return ObjectInfo{}, err
	}

	f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("local_storage: create file %q failed: %w", targetPath, err)
	}
	defer f.Close()

	n, err := io.Copy(f, r)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("local_storage: write file %q failed: %w", targetPath, err)
	}

	return ObjectInfo{
		Key:          key,
		Size:         n,
		ContentType:  contentType,
		LastModified: time.Now(),
	}, nil
}

// Get reads the complete file into memory.
func (s *LocalStorage) Get(ctx context.Context, key string) ([]byte, error) {
	targetPath, err := s.pathFor(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return data, nil
}

// GetStream opens the local file for reading.
func (s *LocalStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	targetPath, err := s.pathFor(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	f, err := os.Open(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ObjectInfo{}, ErrNotFound
		}
		return nil, ObjectInfo{}, err
	}

	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, ObjectInfo{}, err
	}

	return f, ObjectInfo{
		Key:          key,
		Size:         stat.Size(),
		LastModified: stat.ModTime(),
	}, nil
}

// GetReadSeeker opens the local file for random-access reading.
func (s *LocalStorage) GetReadSeeker(ctx context.Context, key string) (ReadSeekCloser, ObjectInfo, error) {
	targetPath, err := s.pathFor(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	f, err := os.Open(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ObjectInfo{}, ErrNotFound
		}
		return nil, ObjectInfo{}, err
	}

	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, ObjectInfo{}, err
	}

	return f, ObjectInfo{
		Key:          key,
		Size:         stat.Size(),
		LastModified: stat.ModTime(),
	}, nil
}

// Delete removes the local file.
func (s *LocalStorage) Delete(ctx context.Context, key string) error {
	targetPath, err := s.pathFor(key)
	if err != nil {
		return err
	}
	err = os.Remove(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeletePrefix removes all files under the prefix path.
func (s *LocalStorage) DeletePrefix(ctx context.Context, prefix string) error {
	targetPath, err := s.pathFor(prefix)
	if err != nil {
		return err
	}
	// If it's a directory or matches prefix, remove all
	err = os.RemoveAll(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Exists reports whether the local file exists.
func (s *LocalStorage) Exists(ctx context.Context, key string) (bool, error) {
	targetPath, err := s.pathFor(key)
	if err != nil {
		return false, err
	}
	_, statErr := os.Stat(targetPath)
	if statErr == nil {
		return true, nil
	}
	if os.IsNotExist(statErr) {
		return false, nil
	}
	return false, statErr
}

// Stat returns metadata about the local file.
func (s *LocalStorage) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	targetPath, err := s.pathFor(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	stat, statErr := os.Stat(targetPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, statErr
	}
	return ObjectInfo{
		Key:          key,
		Size:         stat.Size(),
		LastModified: stat.ModTime(),
	}, nil
}

func (s *LocalStorage) effectiveEndpoint() string {
	base := strings.TrimSpace(s.endpointBase)
	if base == "" {
		return defaultEndpointBase
	}
	return strings.TrimRight(base, "/")
}

func (s *LocalStorage) computeSignature(action, key, filename string, expiresUnix int64) string {
	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write([]byte(fmt.Sprintf("%s\n%s\n%s\n%d", action, key, filename, expiresUnix)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyLocalToken validates the HMAC-SHA256 signature and expiration for a local blob token.
func (s *LocalStorage) VerifyLocalToken(action, key, filename string, expiresUnix int64, sig string) bool {
	if s == nil || len(s.secretKey) == 0 {
		return false
	}
	if sig == "" || action == "" || key == "" || strings.Contains(key, "..") {
		return false
	}
	if expiresUnix < time.Now().Unix() {
		return false
	}
	expectedSig := s.computeSignature(action, key, filename, expiresUnix)
	return hmac.Equal([]byte(expectedSig), []byte(strings.ToLower(sig)))
}

// PresignPut generates a presigned URL for direct upload in local environment.
func (s *LocalStorage) PresignPut(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	if s == nil || s.baseDir == "" {
		return "", errors.New("local_storage: storage is not initialized")
	}
	if strings.TrimSpace(key) == "" {
		return "", errors.New("local_storage: key must not be empty")
	}
	if strings.Contains(key, "..") {
		return "", fmt.Errorf("local_storage: invalid key traversal: %q", key)
	}
	if expires <= 0 {
		return "", errors.New("local_storage: expires must be positive")
	}

	expiresUnix := time.Now().Add(expires).Unix()
	sig := s.computeSignature("put", key, "", expiresUnix)
	endpoint := s.effectiveEndpoint()

	u := fmt.Sprintf("%s/put?key=%s&expires=%d&sig=%s",
		endpoint, url.QueryEscape(key), expiresUnix, sig)
	return u, nil
}

// PresignGet generates a presigned URL for direct download in local environment.
func (s *LocalStorage) PresignGet(ctx context.Context, key, downloadFilename string, expires time.Duration) (string, error) {
	if s == nil || s.baseDir == "" {
		return "", errors.New("local_storage: storage is not initialized")
	}
	if strings.TrimSpace(key) == "" {
		return "", errors.New("local_storage: key must not be empty")
	}
	if strings.Contains(key, "..") {
		return "", fmt.Errorf("local_storage: invalid key traversal: %q", key)
	}
	if expires <= 0 {
		return "", errors.New("local_storage: expires must be positive")
	}

	expiresUnix := time.Now().Add(expires).Unix()
	sig := s.computeSignature("get", key, downloadFilename, expiresUnix)
	endpoint := s.effectiveEndpoint()

	var u string
	if downloadFilename != "" {
		u = fmt.Sprintf("%s/get?key=%s&filename=%s&expires=%d&sig=%s",
			endpoint, url.QueryEscape(key), url.QueryEscape(downloadFilename), expiresUnix, sig)
	} else {
		u = fmt.Sprintf("%s/get?key=%s&expires=%d&sig=%s",
			endpoint, url.QueryEscape(key), expiresUnix, sig)
	}
	return u, nil
}
