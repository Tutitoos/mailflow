package backups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	KeepDaily   = 7
	KeepWeekly  = 4
	KeepMonthly = 12
)

type RunStore interface {
	Begin(context.Context, string, string, time.Time) (Run, error)
	Finish(context.Context, string, string, string, string, int64, int64, time.Time) (Run, error)
	PruneHistory(context.Context) error
}

type ServiceConfig struct {
	Repository             string
	RepositoryPasswordFile string
	AWSAccessKeyIDFile     string
	AWSSecretAccessKeyFile string
	RestoreDatabaseURL     string
	RestoreCDNRoot         string
	RestoreMasterKeyOutput string
}

type Service struct {
	store  RunStore
	stager *Stager
	runner CommandRunner
	config ServiceConfig
	now    func() time.Time
	metric MetricRecorder
}

type MetricRecorder interface {
	Add(string, float64, map[string]string) error
	Observe(string, float64, map[string]string) error
	Set(string, string, float64, map[string]string) error
}

func NewService(store RunStore, stager *Stager, runner CommandRunner, config ServiceConfig) (*Service, error) {
	if store == nil || stager == nil || runner == nil || strings.TrimSpace(config.Repository) == "" {
		return nil, ErrUnavailable
	}
	if err := safeAbsolutePath(config.RepositoryPasswordFile); err != nil {
		return nil, err
	}
	return &Service{store: store, stager: stager, runner: runner, config: config, now: time.Now}, nil
}

func (service *Service) SetMetrics(metric MetricRecorder) { service.metric = metric }

func (service *Service) RepositoryKind() string {
	if strings.HasPrefix(service.config.Repository, "s3:") {
		return "s3"
	}
	return "local"
}

func (service *Service) RunOnce(ctx context.Context, trigger string, scheduledFor time.Time) (completed Run, resultErr error) {
	started := service.now().UTC()
	run, err := service.store.Begin(ctx, trigger, service.RepositoryKind(), scheduledFor)
	if err != nil {
		return Run{}, err
	}
	manifest := Manifest{}
	errorCode := "staging_failed"
	defer func() {
		_ = service.stager.Cleanup()
		if resultErr == nil {
			service.observeRun("success", started, manifest)
			return
		}
		finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		completed, _ = service.store.Finish(finishContext, run.ID, "failed", "", errorCode, manifest.TotalFiles, manifest.TotalBytes, service.now().UTC())
		service.observeRun("failure", started, manifest)
	}()

	manifest, err = service.stager.Prepare(ctx, service.now().UTC())
	if err != nil {
		if errors.Is(err, ErrCommandFailed) {
			errorCode = "dump_failed"
		}
		return completed, err
	}
	environment, err := service.resticEnvironment()
	if err != nil {
		errorCode = "repository_failed"
		return completed, ErrCommandFailed
	}
	if _, err = service.runner.Run(ctx, Command{Name: "restic", Args: []string{"snapshots", "--json", "--latest", "1"}, Env: environment}); err != nil {
		if _, err = service.runner.Run(ctx, Command{Name: "restic", Args: []string{"init"}, Env: environment}); err != nil {
			errorCode = "repository_failed"
			return completed, err
		}
	}
	errorCode = "snapshot_failed"
	output, err := service.runner.Run(ctx, Command{
		Name: "restic", Dir: service.stager.config.Root, Env: environment,
		Args: []string{"backup", "--json", "--quiet", "--host", "mailflow", "--tag", "mailflow", "."},
	})
	if err != nil {
		return completed, err
	}
	snapshotID, err := parseSnapshotID(output)
	if err != nil {
		return completed, err
	}
	errorCode = "verification_failed"
	if _, err = service.runner.Run(ctx, Command{Name: "restic", Args: []string{"check", "--read-data-subset=5%"}, Env: environment}); err != nil {
		return completed, err
	}
	errorCode = "retention_failed"
	if _, err = service.runner.Run(ctx, Command{Name: "restic", Args: []string{
		"forget", "--tag", "mailflow", "--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "12", "--prune",
	}, Env: environment}); err != nil {
		return completed, err
	}
	completed, err = service.store.Finish(ctx, run.ID, "succeeded", snapshotID, "", manifest.TotalFiles, manifest.TotalBytes, service.now().UTC())
	if err != nil {
		return Run{}, err
	}
	_ = service.store.PruneHistory(ctx)
	return completed, nil
}

func (service *Service) observeRun(result string, started time.Time, manifest Manifest) {
	if service.metric == nil {
		return
	}
	labels := map[string]string{"service": "backup", "module": "backups", "operation": "snapshot", "result": result}
	_ = service.metric.Add("mailflow_backup_runs_total", 1, labels)
	_ = service.metric.Observe("mailflow_backup_duration_seconds", service.now().UTC().Sub(started).Seconds(), labels)
	if result == "success" {
		_ = service.metric.Set("mailflow_backup_last_success_unixtime", "gauge", float64(service.now().UTC().Unix()), map[string]string{"service": "backup", "module": "backups"})
		_ = service.metric.Set("mailflow_backup_snapshot_bytes", "gauge", float64(manifest.TotalBytes), map[string]string{"service": "backup", "module": "backups"})
	}
}

func (service *Service) Restore(ctx context.Context, snapshotID, target string, apply bool) (Manifest, error) {
	if !snapshotPattern.MatchString(snapshotID) || safeAbsolutePath(target) != nil {
		return Manifest{}, ErrInvalidPath
	}
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return Manifest{}, ErrInvalidPath
		}
	} else if err != nil || len(entries) != 0 {
		return Manifest{}, ErrInvalidSource
	}
	environment, err := service.resticEnvironment()
	if err != nil {
		return Manifest{}, ErrCommandFailed
	}
	if _, err := service.runner.Run(ctx, Command{Name: "restic", Args: []string{"restore", snapshotID, "--target", target}, Env: environment}); err != nil {
		return Manifest{}, err
	}
	root, err := restoredRoot(target)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := readManifest(filepath.Join(root, "manifest.json"))
	if err != nil || manifest.Version != ManifestVersion {
		return Manifest{}, ErrInvalidSource
	}
	dump := filepath.Join(root, "database", "mailflow.dump")
	digest, err := fileSHA256(dump)
	if err != nil || digest != manifest.DatabaseSHA256 {
		return Manifest{}, ErrInvalidSource
	}
	key := filepath.Join(root, "secrets", "master_key")
	if info, err := os.Stat(key); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return Manifest{}, ErrInvalidSource
	}
	cdnFiles, cdnBytes, err := inspectTree(ctx, filepath.Join(root, "cdn"), manifest.CDNFiles+1, manifest.CDNBytes+1)
	if err != nil || cdnFiles != manifest.CDNFiles || cdnBytes != manifest.CDNBytes {
		return Manifest{}, ErrInvalidSource
	}
	if !apply {
		return manifest, nil
	}
	if service.config.RestoreDatabaseURL == "" || safeAbsolutePath(service.config.RestoreCDNRoot) != nil || safeAbsolutePath(service.config.RestoreMasterKeyOutput) != nil {
		return Manifest{}, ErrInvalidSource
	}
	cdnEntries, cdnErr := os.ReadDir(service.config.RestoreCDNRoot)
	if cdnErr == nil && len(cdnEntries) != 0 {
		return Manifest{}, ErrInvalidSource
	}
	if cdnErr != nil && !errors.Is(cdnErr, os.ErrNotExist) {
		return Manifest{}, ErrInvalidSource
	}
	databaseEnvironment, err := postgresEnvironment(service.config.RestoreDatabaseURL)
	if err != nil {
		return Manifest{}, ErrInvalidSource
	}
	if _, err := service.runner.Run(ctx, Command{Name: "pg_restore", Args: []string{"--no-owner", "--no-privileges", "--exit-on-error", dump}, Env: databaseEnvironment}); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(service.config.RestoreCDNRoot, 0o700); err != nil {
		return Manifest{}, ErrInvalidSource
	}
	if _, _, err := copyTree(ctx, filepath.Join(root, "cdn"), service.config.RestoreCDNRoot, manifest.CDNFiles+1, manifest.CDNBytes+1); err != nil {
		return Manifest{}, err
	}
	if err := copyFile(key, service.config.RestoreMasterKeyOutput, 0o600); err != nil {
		return Manifest{}, ErrInvalidSource
	}
	return manifest, nil
}

func (service *Service) resticEnvironment() (map[string]string, error) {
	environment := map[string]string{
		"RESTIC_REPOSITORY":    service.config.Repository,
		"RESTIC_PASSWORD_FILE": service.config.RepositoryPasswordFile,
	}
	files := []struct{ key, path string }{
		{"AWS_ACCESS_KEY_ID", service.config.AWSAccessKeyIDFile},
		{"AWS_SECRET_ACCESS_KEY", service.config.AWSSecretAccessKeyFile},
	}
	for _, item := range files {
		if item.path == "" {
			continue
		}
		value, err := readSecret(item.path)
		if err != nil || value == "" {
			return nil, ErrCommandFailed
		}
		environment[item.key] = value
	}
	return environment, nil
}

func parseSnapshotID(output []byte) (string, error) {
	for _, line := range strings.Split(string(output), "\n") {
		var event struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.MessageType == "summary" && snapshotPattern.MatchString(event.SnapshotID) {
			return event.SnapshotID, nil
		}
	}
	return "", ErrCommandFailed
}

func restoredRoot(target string) (string, error) {
	if _, err := os.Stat(filepath.Join(target, "manifest.json")); err == nil {
		return target, nil
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return "", ErrInvalidSource
	}
	root := filepath.Join(target, entries[0].Name())
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err != nil {
		return "", ErrInvalidSource
	}
	return root, nil
}

func readManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	var manifest Manifest
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return manifest, nil
}

func inspectTree(ctx context.Context, root string, maxFiles, maxBytes int64) (int64, int64, error) {
	var files, bytes int64
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return ErrInvalidSource
		}
		files++
		bytes += info.Size()
		if files > maxFiles || bytes > maxBytes {
			return ErrStagingLimit
		}
		return nil
	})
	return files, bytes, err
}
