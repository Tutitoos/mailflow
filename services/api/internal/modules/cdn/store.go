package cdn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrInvalidObjectID   = errors.New("invalid object id")
	ErrInvalidMediaType  = errors.New("invalid object media type")
	ErrMediaTypeMismatch = errors.New("object media type does not match its content")
	ErrObjectTooLarge    = errors.New("object exceeds configured size limit")
	objectIDPattern      = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type Store struct {
	root     string
	maxBytes int64
}

type ObjectInfo struct {
	Path      string
	MediaType string
	Size      int64
	ETag      string
}

func NewStore(root string, maxBytes int64) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("CDN root must be absolute")
	}
	if maxBytes <= 0 {
		return nil, errors.New("CDN size limit must be positive")
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
	info, err := s.PutValidated(namespace, id, "application/octet-stream", source)
	return info.Path, err
}

func (s *Store) PutValidated(namespace, id, declaredMediaType string, source io.Reader) (ObjectInfo, error) {
	destination, err := s.objectPath(namespace, id)
	if err != nil {
		return ObjectInfo{}, err
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(declaredMediaType))
	if err != nil || mediaType == "" {
		return ObjectInfo{}, ErrInvalidMediaType
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return ObjectInfo{}, fmt.Errorf("create object directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".mailflow-upload-*")
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("create temporary object: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	header := make([]byte, 512)
	headerSize, readErr := io.ReadFull(source, header)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		_ = temporary.Close()
		return ObjectInfo{}, fmt.Errorf("read object header: %w", readErr)
	}
	header = header[:headerSize]
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(header))
	if mediaType != "application/octet-stream" && detected != mediaType {
		_ = temporary.Close()
		return ObjectInfo{}, ErrMediaTypeMismatch
	}
	if mediaType == "application/octet-stream" {
		mediaType = detected
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), io.LimitReader(io.MultiReader(bytes.NewReader(header), source), s.maxBytes+1))
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if copyErr != nil {
		return ObjectInfo{}, fmt.Errorf("write object: %w", copyErr)
	}
	if syncErr != nil {
		return ObjectInfo{}, fmt.Errorf("sync object: %w", syncErr)
	}
	if closeErr != nil {
		return ObjectInfo{}, fmt.Errorf("close object: %w", closeErr)
	}
	if written > s.maxBytes {
		return ObjectInfo{}, ErrObjectTooLarge
	}
	if err := os.Chmod(temporaryName, 0o640); err != nil {
		return ObjectInfo{}, fmt.Errorf("protect object: %w", err)
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return ObjectInfo{}, fmt.Errorf("commit object: %w", err)
	}
	return ObjectInfo{Path: destination, MediaType: mediaType, Size: written, ETag: hex.EncodeToString(digest.Sum(nil))}, nil
}

func (s *Store) Open(namespace, id string) (*os.File, error) {
	path, err := s.objectPath(namespace, id)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *Store) Remove(namespace, id string) error {
	path, err := s.objectPath(namespace, id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove object: %w", err)
	}
	return nil
}
