package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config holds configuration for connecting to MinIO or AWS S3.
type S3Config struct {
	Endpoint        string // e.g. "localhost:9000" or "s3.ap-northeast-2.amazonaws.com"
	Region          string // e.g. "us-east-1" or "ap-northeast-2"
	Bucket          string // e.g. "whisper-blobs"
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
}

// S3Storage implements Storage backed by MinIO or AWS S3.
type S3Storage struct {
	client *minio.Client
	bucket string
}

// NewS3Storage instantiates and validates an S3/MinIO storage client.
func NewS3Storage(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	// Strip http:// or https:// prefix if accidentally provided in endpoint
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, errors.New("s3_storage: bucket name must not be empty")
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	opts := &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:    cfg.UseSSL,
		Region:    strings.TrimSpace(cfg.Region),
		Transport: transport,
	}

	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("s3_storage: init client failed: %w", err)
	}

	s := &S3Storage{
		client: client,
		bucket: bucket,
	}

	// Verify bucket existence or create if needed
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
	}

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		// Log warning or return if fatal
		return nil, fmt.Errorf("s3_storage: check bucket %q failed: %w", bucket, err)
	}
	if !exists {
		err = client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: cfg.Region})
		if err != nil {
			return nil, fmt.Errorf("s3_storage: create bucket %q failed: %w", bucket, err)
		}
	}

	return s, nil
}

// Put uploads byte data into S3/MinIO.
func (s *S3Storage) Put(ctx context.Context, key string, data []byte, contentType string) (ObjectInfo, error) {
	return s.PutStream(ctx, key, bytes.NewReader(data), int64(len(data)), contentType)
}

// PutStream streams an io.Reader into S3/MinIO.
func (s *S3Storage) PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (ObjectInfo, error) {
	if s == nil || s.client == nil {
		return ObjectInfo{}, errors.New("s3_storage: storage is not initialized")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	opts := minio.PutObjectOptions{
		ContentType: contentType,
	}
	uploadInfo, err := s.client.PutObject(ctx, s.bucket, key, r, size, opts)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("s3_storage: put %q failed: %w", key, err)
	}

	return ObjectInfo{
		Key:          key,
		Size:         uploadInfo.Size,
		ContentType:  contentType,
		ETag:         uploadInfo.ETag,
		LastModified: time.Now(),
	}, nil
}

// Get fetches the object as a byte slice.
func (s *S3Storage) Get(ctx context.Context, key string) ([]byte, error) {
	r, _, err := s.GetStream(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// GetStream opens an object stream for reading.
func (s *S3Storage) GetStream(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if s == nil || s.client == nil {
		return nil, ObjectInfo{}, errors.New("s3_storage: storage is not initialized")
	}

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("s3_storage: get %q failed: %w", key, err)
	}

	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if isS3NotFound(err) {
			return nil, ObjectInfo{}, ErrNotFound
		}
		return nil, ObjectInfo{}, fmt.Errorf("s3_storage: stat %q failed: %w", key, err)
	}

	info := ObjectInfo{
		Key:          key,
		Size:         stat.Size,
		ContentType:  stat.ContentType,
		ETag:         stat.ETag,
		LastModified: stat.LastModified,
	}
	return obj, info, nil
}

// Delete removes an object by key.
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	if s == nil || s.client == nil {
		return errors.New("s3_storage: storage is not initialized")
	}
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil && !isS3NotFound(err) {
		return fmt.Errorf("s3_storage: delete %q failed: %w", key, err)
	}
	return nil
}

// DeletePrefix removes all objects starting with prefix.
func (s *S3Storage) DeletePrefix(ctx context.Context, prefix string) error {
	if s == nil || s.client == nil {
		return errors.New("s3_storage: storage is not initialized")
	}

	objectsCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objectsCh)
		opts := minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}
		for obj := range s.client.ListObjects(ctx, s.bucket, opts) {
			if obj.Err != nil {
				continue
			}
			objectsCh <- obj
		}
	}()

	errorCh := s.client.RemoveObjects(ctx, s.bucket, objectsCh, minio.RemoveObjectsOptions{})
	var firstErr error
	for e := range errorCh {
		if e.Err != nil && firstErr == nil {
			firstErr = e.Err
		}
	}
	return firstErr
}

// Exists checks whether key exists in S3/MinIO.
func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

// Stat retrieves object metadata without reading object body.
func (s *S3Storage) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if s == nil || s.client == nil {
		return ObjectInfo{}, errors.New("s3_storage: storage is not initialized")
	}

	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, fmt.Errorf("s3_storage: stat %q failed: %w", key, err)
	}

	return ObjectInfo{
		Key:          key,
		Size:         info.Size,
		ContentType:  info.ContentType,
		ETag:         info.ETag,
		LastModified: info.LastModified,
	}, nil
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.Code == "NotFound" || strings.Contains(strings.ToLower(err.Error()), "not found")
}
