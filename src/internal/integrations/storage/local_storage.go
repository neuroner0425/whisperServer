package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalStorage implements Storage on top of a local filesystem directory.
type LocalStorage struct {
	baseDir string
}

// NewLocalStorage instantiates a LocalStorage root in the given directory.
func NewLocalStorage(baseDir string) (*LocalStorage, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("local_storage: base directory must not be empty")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("local_storage: create base dir %q failed: %w", baseDir, err)
	}
	return &LocalStorage{baseDir: baseDir}, nil
}

func (s *LocalStorage) pathFor(key string) string {
	clean := filepath.Clean(key)
	clean = strings.TrimPrefix(clean, "/")
	clean = strings.TrimPrefix(clean, "\\")
	return filepath.Join(s.baseDir, clean)
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
	targetPath := s.pathFor(key)
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
	targetPath := s.pathFor(key)
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
	targetPath := s.pathFor(key)
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
	targetPath := s.pathFor(key)
	err := os.Remove(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeletePrefix removes all files under the prefix path.
func (s *LocalStorage) DeletePrefix(ctx context.Context, prefix string) error {
	targetPath := s.pathFor(prefix)
	// If it's a directory or matches prefix, remove all
	err := os.RemoveAll(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Exists reports whether the local file exists.
func (s *LocalStorage) Exists(ctx context.Context, key string) (bool, error) {
	targetPath := s.pathFor(key)
	_, err := os.Stat(targetPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Stat returns metadata about the local file.
func (s *LocalStorage) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	targetPath := s.pathFor(key)
	stat, err := os.Stat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, err
	}
	return ObjectInfo{
		Key:          key,
		Size:         stat.Size(),
		LastModified: stat.ModTime(),
	}, nil
}
