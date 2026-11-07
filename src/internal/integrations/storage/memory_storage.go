package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

type memoryObject struct {
	data        []byte
	contentType string
	updatedAt   time.Time
}

// MemoryStorage is an in-memory thread-safe implementation of Storage for tests.
type MemoryStorage struct {
	mu      sync.RWMutex
	objects map[string]memoryObject
}

// NewMemoryStorage instantiates an in-memory storage driver.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		objects: make(map[string]memoryObject),
	}
}

// Put saves data into memory.
func (s *MemoryStorage) Put(ctx context.Context, key string, data []byte, contentType string) (ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := make([]byte, len(data))
	copy(cp, data)
	now := time.Now()
	s.objects[key] = memoryObject{
		data:        cp,
		contentType: contentType,
		updatedAt:   now,
	}

	return ObjectInfo{
		Key:          key,
		Size:         int64(len(data)),
		ContentType:  contentType,
		LastModified: now,
	}, nil
}

// PutStream reads data into memory.
func (s *MemoryStorage) PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (ObjectInfo, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return ObjectInfo{}, err
	}
	return s.Put(ctx, key, data, contentType)
}

// Get fetches byte slice from memory.
func (s *MemoryStorage) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(obj.data))
	copy(cp, obj.data)
	return cp, nil
}

// GetStream returns an in-memory ReadCloser.
func (s *MemoryStorage) GetStream(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[key]
	if !ok {
		return nil, ObjectInfo{}, ErrNotFound
	}

	info := ObjectInfo{
		Key:          key,
		Size:         int64(len(obj.data)),
		ContentType:  obj.contentType,
		LastModified: obj.updatedAt,
	}
	return io.NopCloser(bytes.NewReader(obj.data)), info, nil
}

// GetReadSeeker returns an in-memory ReadSeekCloser.
func (s *MemoryStorage) GetReadSeeker(ctx context.Context, key string) (ReadSeekCloser, ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[key]
	if !ok {
		return nil, ObjectInfo{}, ErrNotFound
	}

	info := ObjectInfo{
		Key:          key,
		Size:         int64(len(obj.data)),
		ContentType:  obj.contentType,
		LastModified: obj.updatedAt,
	}
	return NopReadSeekCloser(bytes.NewReader(obj.data)), info, nil
}

// Delete removes object by key.
func (s *MemoryStorage) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// DeletePrefix removes all objects matching prefix.
func (s *MemoryStorage) DeletePrefix(ctx context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) {
			delete(s.objects, k)
		}
	}
	return nil
}

// Exists reports whether key exists.
func (s *MemoryStorage) Exists(ctx context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.objects[key]
	return ok, nil
}

// Stat returns metadata about object.
func (s *MemoryStorage) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[key]
	if !ok {
		return ObjectInfo{}, ErrNotFound
	}
	return ObjectInfo{
		Key:          key,
		Size:         int64(len(obj.data)),
		ContentType:  obj.contentType,
		LastModified: obj.updatedAt,
	}, nil
}

// PresignPut returns a mock presigned upload URL for testing.
func (s *MemoryStorage) PresignPut(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("memory_storage: key must not be empty")
	}
	return "memory://put/" + key, nil
}

// PresignGet returns a mock presigned download URL for testing.
func (s *MemoryStorage) PresignGet(ctx context.Context, key, downloadFilename string, expires time.Duration) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("memory_storage: key must not be empty")
	}
	if downloadFilename != "" {
		return fmt.Sprintf("memory://get/%s?filename=%s", key, url.QueryEscape(downloadFilename)), nil
	}
	return "memory://get/" + key, nil
}
