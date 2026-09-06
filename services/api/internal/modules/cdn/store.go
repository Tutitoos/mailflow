package cdn

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrInvalidObjectID = errors.New("invalid object id")
	ErrObjectTooLarge  = errors.New("object exceeds configured size limit")
	objectIDPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type Store struct {
	root     string
	maxBytes int64
}

func NewStore(root string, maxBytes int64) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("CDN root must be absolute")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create CDN root: %w", err)
	}
	return &Store{root: filepath.Clean(root), maxBytes: maxBytes}, nil
}

func normalizeID(id string) (string, error) {
	id = strings.ToLower(strings.ReplaceAll(id, "-", ""))
	if !objectIDPattern.MatchString(id) {
		return "", ErrInvalidObjectID
	}
	return id, nil
}

func (s *Store) objectPath(namespace, id string) (string, error) {
	if namespace != "attachments" && namespace != "sentry" {
		return "", errors.New("invalid CDN namespace")
	}
	normalized, err := normalizeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, namespace, normalized[:2], normalized[2:4], normalized), nil
}

func (s *Store) Put(namespace, id string, source io.Reader) (string, error) {
	destination, err := s.objectPath(namespace, id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return "", fmt.Errorf("create object directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".mailflow-upload-*")
	if err != nil {
		return "", fmt.Errorf("create temporary object: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	written, copyErr := io.Copy(temporary, io.LimitReader(source, s.maxBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return "", fmt.Errorf("write object: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close object: %w", closeErr)
	}
	if written > s.maxBytes {
		return "", ErrObjectTooLarge
	}
	if err := os.Chmod(temporaryName, 0o640); err != nil {
		return "", fmt.Errorf("protect object: %w", err)
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return "", fmt.Errorf("commit object: %w", err)
	}
	return destination, nil
}

func (s *Store) Open(namespace, id string) (*os.File, error) {
	path, err := s.objectPath(namespace, id)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}
