package photostore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	metaWidth  = "width"
	metaHeight = "height"
)

type S3 struct {
	client *minio.Client
	bucket string
}

var _ Store = (*S3)(nil)

func NewS3(ctx context.Context, cfg Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("photostore: s3 backend needs an endpoint and bucket")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("photostore: init s3 client: %w", err)
	}
	s := &S3{client: client, bucket: cfg.Bucket}
	if err := s.ensureBucket(ctx, cfg.Region); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *S3) ensureBucket(ctx context.Context, region string) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("photostore: check bucket %s: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		// A racing writer may have created it between the two calls.
		if exists, chkErr := s.client.BucketExists(ctx, s.bucket); chkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("photostore: create bucket %s: %w", s.bucket, err)
	}
	return nil
}

func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	if _, err := cleanKey(key); err != nil {
		return Info{}, err
	}
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if notFound(err) {
			return Info{}, nil
		}
		return Info{}, fmt.Errorf("photostore: stat %s: %w", key, err)
	}
	// StatObject returns user metadata as response headers ("x-amz-meta-<key>").
	w, _ := strconv.Atoi(info.Metadata.Get("X-Amz-Meta-" + metaWidth))
	h, _ := strconv.Atoi(info.Metadata.Get("X-Amz-Meta-" + metaHeight))
	return Info{Exists: true, Width: w, Height: h}, nil
}

// readTimeout bounds one object read: minio's transport caps dial and headers
// but not a stalled body, and a stuck /img handler would otherwise hang.
const readTimeout = 15 * time.Second

func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	if _, err := cleanKey(key); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("photostore: get %s: %w", key, err)
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		if notFound(err) {
			return nil, fmt.Errorf("photostore: get %s: %w", key, os.ErrNotExist)
		}
		return nil, fmt.Errorf("photostore: read %s: %w", key, err)
	}
	return data, nil
}

func (s *S3) Put(ctx context.Context, key string, data []byte, width, height int) error {
	if _, err := cleanKey(key); err != nil {
		return err
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{
			ContentType: jpegMIME,
			UserMetadata: map[string]string{
				metaWidth:  strconv.Itoa(width),
				metaHeight: strconv.Itoa(height),
			},
		})
	if err != nil {
		return fmt.Errorf("photostore: put %s: %w", key, err)
	}
	return nil
}

// Delete removes the object, treating an already-absent key as success so the
// TTL/delist sweeps are idempotent.
func (s *S3) Delete(ctx context.Context, key string) error {
	if _, err := cleanKey(key); err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		if notFound(err) {
			return nil
		}
		return fmt.Errorf("photostore: delete %s: %w", key, err)
	}
	return nil
}

// A missing object surfaces as NoSuchKey on most backends and a bare 404 on a
// few; check both so write-once works across S3-compatible servers.
func notFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.StatusCode == http.StatusNotFound
}
