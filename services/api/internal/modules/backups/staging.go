package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ManifestVersion = 1

var (
	ErrInvalidPath   = errors.New("backup path is unsafe")
	ErrStagingLimit  = errors.New("backup staging limit exceeded")
	ErrInvalidSource = errors.New("backup source is invalid")
)

type StagingConfig struct {
	Root          string
	CDNRoot       string
	MasterKeyFile string
	DatabaseURL   string
	MaxFiles      int64
	MaxBytes      int64
}

type Manifest struct {
	Version        int       `json:"version"`
	CreatedAt      time.Time `json:"createdAt"`
	DatabaseSHA256 string    `json:"databaseSha256"`
	CDNFiles       int64     `json:"cdnFiles"`
	CDNBytes       int64     `json:"cdnBytes"`
	TotalFiles     int64     `json:"totalFiles"`
	TotalBytes     int64     `json:"totalBytes"`
}

type Stager struct {
	config StagingConfig
	runner CommandRunner
}

func NewStager(config StagingConfig, runner CommandRunner) (*Stager, error) {
	if runner == nil || config.DatabaseURL == "" || config.MasterKeyFile == "" || config.MaxFiles <= 0 || config.MaxBytes <= 0 {
		return nil, ErrInvalidSource
	}
	for _, path := range []string{config.Root, config.CDNRoot, config.MasterKeyFile} {
		if err := safeAbsolutePath(path); err != nil {
			return nil, err
		}
	}
	return &Stager{config: config, runner: runner}, nil
}

func (stager *Stager) Prepare(ctx context.Context, now time.Time) (Manifest, error) {
	if err := resetDirectory(stager.config.Root); err != nil {
		return Manifest{}, fmt.Errorf("%w: prepare staging root", ErrInvalidSource)
	}
	for _, name := range []string{"database", "cdn", "secrets"} {
		if err := os.MkdirAll(filepath.Join(stager.config.Root, name), 0o700); err != nil {
			return Manifest{}, fmt.Errorf("%w: prepare staging directory", ErrInvalidSource)
		}
	}
	dumpPath := filepath.Join(stager.config.Root, "database", "mailflow.dump")
	databaseEnvironment, err := postgresEnvironment(stager.config.DatabaseURL)
	if err != nil {
		return Manifest{}, err
	}
	_, err = stager.runner.Run(ctx, Command{
		Name: "pg_dump",
		Args: []string{"--format=custom", "--no-owner", "--no-privileges", "--file", dumpPath},
		Env:  databaseEnvironment,
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("dump database: %w", err)
	}
	dumpInfo, err := os.Stat(dumpPath)
	if err != nil || !dumpInfo.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("%w: database dump", ErrInvalidSource)
	}
	dumpHash, err := fileSHA256(dumpPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: hash database dump", ErrInvalidSource)
	}
	keyInfo, err := os.Stat(stager.config.MasterKeyFile)
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Size() == 0 {
		return Manifest{}, fmt.Errorf("%w: master key", ErrInvalidSource)
	}
	if err := copyFile(stager.config.MasterKeyFile, filepath.Join(stager.config.Root, "secrets", "master_key"), 0o600); err != nil {
		return Manifest{}, fmt.Errorf("%w: stage master key", ErrInvalidSource)
	}
	manifest := Manifest{
		Version:        ManifestVersion,
		CreatedAt:      now.UTC(),
		DatabaseSHA256: dumpHash,
		TotalFiles:     2,
		TotalBytes:     dumpInfo.Size() + keyInfo.Size(),
	}
	if manifest.TotalFiles > stager.config.MaxFiles || manifest.TotalBytes > stager.config.MaxBytes {
		return Manifest{}, ErrStagingLimit
	}
	files, bytes, err := copyTree(ctx, stager.config.CDNRoot, filepath.Join(stager.config.Root, "cdn"), stager.config.MaxFiles-manifest.TotalFiles, stager.config.MaxBytes-manifest.TotalBytes)
	if err != nil {
		return Manifest{}, err
	}
	manifest.CDNFiles = files
	manifest.CDNBytes = bytes
	manifest.TotalFiles += files
	manifest.TotalBytes += bytes
	manifest.TotalFiles++
	if err := writeJSONAtomic(filepath.Join(stager.config.Root, "manifest.json"), manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: write manifest", ErrInvalidSource)
	}
	return manifest, nil
}

func (stager *Stager) Cleanup() error {
	return emptyDirectory(stager.config.Root)
}

func copyTree(ctx context.Context, source, target string, maxFiles, maxBytes int64) (int64, int64, error) {
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return 0, 0, fmt.Errorf("%w: CDN root", ErrInvalidSource)
	}
	var files, bytes int64
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("%w: read CDN", ErrInvalidSource)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrInvalidPath
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: CDN object", ErrInvalidSource)
		}
		files++
		bytes += info.Size()
		if files > maxFiles || bytes > maxBytes {
			return ErrStagingLimit
		}
		return copyFile(path, destination, 0o600)
	})
	return files, bytes, err
}

func copyFile(source, target string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temporary := target + ".tmp"
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return os.Rename(temporary, target)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeJSONAtomic(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func resetDirectory(path string) error {
	return emptyDirectory(path)
}

func emptyDirectory(path string) error {
	if err := safeAbsolutePath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o700)
	}
	if err != nil || !info.IsDir() {
		return ErrInvalidSource
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func safeAbsolutePath(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == string(filepath.Separator) || clean == "." || len(strings.Split(strings.Trim(clean, string(filepath.Separator)), string(filepath.Separator))) < 2 {
		return ErrInvalidPath
	}
	return nil
}

func postgresEnvironment(databaseURL string) (map[string]string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.User == nil || parsed.Hostname() == "" {
		return nil, ErrInvalidSource
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if database == "" || strings.Contains(database, "/") {
		return nil, ErrInvalidSource
	}
	password, hasPassword := parsed.User.Password()
	if parsed.User.Username() == "" || !hasPassword || password == "" {
		return nil, ErrInvalidSource
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	environment := map[string]string{
		"PGHOST":     parsed.Hostname(),
		"PGPORT":     port,
		"PGUSER":     parsed.User.Username(),
		"PGDATABASE": database,
		"PGPASSWORD": password,
	}
	if sslMode := parsed.Query().Get("sslmode"); sslMode != "" {
		environment["PGSSLMODE"] = sslMode
	}
	return environment, nil
}
