package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned when the requested object key does not exist.
var ErrNotFound = errors.New("storage: object not found")

// ObjectInfo captures metadata about a stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

// Storage is the unified contract for blob and object persistence.
type Storage interface {
	// Put stores binary data under the given key.
	Put(ctx context.Context, key string, data []byte, contentType string) (ObjectInfo, error)

	// PutStream streams binary data of known or unknown (-1) size under key.
	PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (ObjectInfo, error)

	// Get loads the complete object into memory.
	Get(ctx context.Context, key string) ([]byte, error)

	// GetStream opens a stream to the object. Callers must close the returned ReadCloser.
	GetStream(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)

	// Delete removes a single object by key.
	Delete(ctx context.Context, key string) error

	// DeletePrefix removes all objects matching key prefix.
	DeletePrefix(ctx context.Context, prefix string) error

	// Exists reports whether the key exists.
	Exists(ctx context.Context, key string) (bool, error)

	// Stat returns metadata about the key without reading the body.
	Stat(ctx context.Context, key string) (ObjectInfo, error)
}
