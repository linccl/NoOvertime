package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

const (
	BackendLocal = "local"
	BackendOSS   = "oss"
)

type Options struct {
	Backend            string
	LocalDir           string
	PublicBaseURL      string
	OSSEndpoint        string
	OSSBucket          string
	OSSAccessKeyID     string
	OSSAccessKeySecret string
	OSSPrefix          string
}

type PutRequest struct {
	Key         string
	Body        io.Reader
	ContentType string
}

type PutResult struct {
	Key string
	URL string
}

type ObjectStore interface {
	Put(ctx context.Context, req PutRequest) (PutResult, error)
	Delete(ctx context.Context, key string) error
}

func NewStore(opts Options) (ObjectStore, string, error) {
	backend := strings.ToLower(strings.TrimSpace(opts.Backend))
	if backend == "" {
		backend = BackendLocal
	}

	switch backend {
	case BackendLocal:
		dir := strings.TrimSpace(opts.LocalDir)
		if dir == "" {
			dir = "./uploads-data"
		}
		return NewLocalStore(dir, opts.PublicBaseURL), dir, nil
	case BackendOSS:
		store, err := NewOSSStore(opts)
		if err != nil {
			return nil, "", err
		}
		return store, "", nil
	default:
		return nil, "", fmt.Errorf("unsupported upload storage backend %q", backend)
	}
}

func normalizeObjectKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("object key is empty")
	}
	if strings.ContainsRune(key, '\x00') {
		return "", fmt.Errorf("object key contains null byte")
	}

	cleaned := path.Clean(strings.ReplaceAll(key, "\\", "/"))
	switch {
	case cleaned == ".":
		return "", fmt.Errorf("object key is empty")
	case cleaned == "..":
		return "", fmt.Errorf("object key must not escape root")
	case strings.HasPrefix(cleaned, "../"):
		return "", fmt.Errorf("object key must not escape root")
	}

	return strings.TrimPrefix(cleaned, "/"), nil
}

func joinPublicURL(baseURL, objectKey string) string {
	escaped := escapeObjectKey(objectKey)
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return "/" + escaped
	}
	return trimmedBase + "/" + escaped
}

func escapeObjectKey(objectKey string) string {
	parts := strings.Split(strings.TrimPrefix(objectKey, "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
