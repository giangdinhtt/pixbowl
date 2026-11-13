package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/giangdinhtt/pixbowl/internal/config"
	"github.com/giangdinhtt/pixbowl/internal/id"
)

// S3 stores objects in any S3-compatible service (AWS, R2, MinIO, B2).
// Layout mirrors the local backend: <prefix><id>/image.webp, .../meta.json, .../v/<key>.
type S3 struct {
	client *minio.Client
	bucket string
	prefix string
}

func newS3(ctx context.Context, cfg config.S3Config) (*S3, error) {
	endpoint, secure, err := parseEndpoint(cfg.Endpoint, cfg.UseSSL)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}

	s := &S3{client: client, bucket: cfg.Bucket, prefix: cfg.Prefix}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3: cannot reach bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("s3: cannot create bucket %q: %w", cfg.Bucket, err)
		}
	}
	return s, nil
}

// parseEndpoint accepts both "https://host" and "host:9000" forms.
func parseEndpoint(raw string, useSSL bool) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, errors.New("s3: S3_ENDPOINT_URL is empty")
	}
	if !strings.Contains(raw, "://") {
		return raw, useSSL, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("s3: invalid endpoint %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", false, fmt.Errorf("s3: invalid endpoint %q", raw)
	}
	return u.Host, u.Scheme == "https", nil
}

func (s *S3) key(imageID, rel string) (string, error) {
	if !id.Valid(imageID) {
		return "", ErrInvalidID
	}
	if !validRel(rel) {
		return "", ErrInvalidKey
	}
	return s.prefix + imageID + "/" + rel, nil
}

func (s *S3) Init(context.Context) error { return nil }

func (s *S3) Put(ctx context.Context, imageID, rel string, data []byte) error {
	key, err := s.key(imageID, rel)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType(rel),
	})
	return err
}

func (s *S3) PutMeta(ctx context.Context, meta *Meta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return s.Put(ctx, meta.ID, "meta.json", data)
}

func (s *S3) Meta(ctx context.Context, imageID string) (*Meta, error) {
	key, err := s.key(imageID, "meta.json")
	if err != nil {
		return nil, err
	}
	// GetObject is lazy, so the first Read is what reports a missing key.
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer object.Close()

	data, err := io.ReadAll(io.LimitReader(object, 1<<20))
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return decodeMeta(data)
}

func (s *S3) Open(ctx context.Context, imageID, rel string) (io.ReadSeekCloser, int64, error) {
	key, err := s.key(imageID, rel)
	if err != nil {
		return nil, 0, err
	}
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	return object, info.Size, nil
}

func (s *S3) Delete(ctx context.Context, imageID string) error {
	if !id.Valid(imageID) {
		return ErrInvalidID
	}
	prefix := s.prefix + imageID + "/"

	objects := make([]minio.ObjectInfo, 0, 4)
	for info := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if info.Err != nil {
			return info.Err
		}
		objects = append(objects, info)
	}
	if len(objects) == 0 {
		return nil
	}

	ch := make(chan minio.ObjectInfo, len(objects))
	for _, info := range objects {
		ch <- info
	}
	close(ch)

	for err := range s.client.RemoveObjects(ctx, s.bucket, ch, minio.RemoveObjectsOptions{}) {
		if err.Err != nil {
			return err.Err
		}
	}
	return nil
}

func (s *S3) Healthy(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}

func isS3NotFound(err error) bool {
	var response minio.ErrorResponse
	if errors.As(err, &response) {
		return response.Code == "NoSuchKey" || response.Code == "NotFound" || response.StatusCode == 404
	}
	return false
}

func contentType(rel string) string {
	switch path.Ext(rel) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".gif":
		return "image/gif"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}
