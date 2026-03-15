package storage

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type OSSStore struct {
	bucket        *oss.Bucket
	publicBaseURL string
	bucketName    string
	endpoint      string
	prefix        string
}

func NewOSSStore(opts Options) (*OSSStore, error) {
	endpoint := strings.TrimSpace(opts.OSSEndpoint)
	bucketName := strings.TrimSpace(opts.OSSBucket)
	accessKeyID := strings.TrimSpace(opts.OSSAccessKeyID)
	accessKeySecret := strings.TrimSpace(opts.OSSAccessKeySecret)

	if endpoint == "" {
		return nil, fmt.Errorf("upload oss endpoint is required")
	}
	if bucketName == "" {
		return nil, fmt.Errorf("upload oss bucket is required")
	}
	if accessKeyID == "" || accessKeySecret == "" {
		return nil, fmt.Errorf("upload oss credentials are required")
	}

	client, err := oss.New(endpoint, accessKeyID, accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("create oss client: %w", err)
	}
	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("open oss bucket: %w", err)
	}

	return &OSSStore{
		bucket:        bucket,
		publicBaseURL: strings.TrimSpace(opts.PublicBaseURL),
		bucketName:    bucketName,
		endpoint:      endpoint,
		prefix:        strings.Trim(strings.TrimSpace(opts.OSSPrefix), "/"),
	}, nil
}

func (s *OSSStore) Put(_ context.Context, req PutRequest) (PutResult, error) {
	key, err := normalizeObjectKey(req.Key)
	if err != nil {
		return PutResult{}, err
	}
	if s.prefix != "" {
		key = path.Join(s.prefix, key)
	}

	options := []oss.Option{}
	if contentType := strings.TrimSpace(req.ContentType); contentType != "" {
		options = append(options, oss.ContentType(contentType))
	}

	if err := s.bucket.PutObject(key, req.Body, options...); err != nil {
		return PutResult{}, fmt.Errorf("put oss object: %w", err)
	}

	return PutResult{
		Key: key,
		URL: s.publicURL(key),
	}, nil
}

func (s *OSSStore) Delete(_ context.Context, key string) error {
	safeKey, err := normalizeObjectKey(key)
	if err != nil {
		return err
	}
	if err := s.bucket.DeleteObject(safeKey); err != nil {
		return fmt.Errorf("delete oss object: %w", err)
	}
	return nil
}

func (s *OSSStore) publicURL(objectKey string) string {
	if s.publicBaseURL != "" {
		return joinPublicURL(s.publicBaseURL, objectKey)
	}

	endpoint := strings.TrimSpace(s.endpoint)
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return joinPublicURL(endpoint, objectKey)
	}

	scheme := parsed.Scheme
	if scheme == "" {
		scheme = "https"
	}
	host := parsed.Host
	if host == "" {
		host = strings.Trim(parsed.Path, "/")
	}
	if host == "" {
		return joinPublicURL(endpoint, objectKey)
	}

	return joinPublicURL(fmt.Sprintf("%s://%s.%s", scheme, s.bucketName, host), objectKey)
}
