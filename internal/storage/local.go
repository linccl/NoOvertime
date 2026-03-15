package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const localUploadsRoutePrefix = "/uploads"

type LocalStore struct {
	rootDir       string
	publicBaseURL string
}

func NewLocalStore(rootDir, publicBaseURL string) *LocalStore {
	return &LocalStore{
		rootDir:       rootDir,
		publicBaseURL: publicBaseURL,
	}
}

func (s *LocalStore) Put(_ context.Context, req PutRequest) (PutResult, error) {
	key, err := normalizeObjectKey(req.Key)
	if err != nil {
		return PutResult{}, err
	}

	targetPath := filepath.Join(s.rootDir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return PutResult{}, fmt.Errorf("create local upload dir: %w", err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), ".upload-*")
	if err != nil {
		return PutResult{}, fmt.Errorf("create temp upload file: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := io.Copy(tempFile, req.Body); err != nil {
		tempFile.Close()
		return PutResult{}, fmt.Errorf("write local upload file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return PutResult{}, fmt.Errorf("close local upload file: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return PutResult{}, fmt.Errorf("move local upload file: %w", err)
	}

	return PutResult{
		Key: key,
		URL: joinPublicURL(s.publicBaseURL+localUploadsRoutePrefix, key),
	}, nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	safeKey, err := normalizeObjectKey(key)
	if err != nil {
		return err
	}

	targetPath := filepath.Join(s.rootDir, filepath.FromSlash(safeKey))
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete local upload file: %w", err)
	}
	return nil
}
