package backups

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	commands      []Command
	failName      string
	restoreSource string
}

func (runner *fakeRunner) Run(_ context.Context, command Command) ([]byte, error) {
	runner.commands = append(runner.commands, command)
	if command.Name == runner.failName {
		return nil, ErrCommandFailed
	}
	if command.Name == "pg_dump" {
		return nil, os.WriteFile(command.Args[len(command.Args)-1], []byte("database dump"), 0o600)
	}
	if command.Name == "restic" && len(command.Args) > 0 && command.Args[0] == "restore" && runner.restoreSource != "" {
		target := command.Args[len(command.Args)-1]
		if err := os.MkdirAll(target, 0o700); err != nil {
			return nil, err
		}
		_, _, err := copyTree(context.Background(), runner.restoreSource, target, 100, 1<<20)
		return nil, err
	}
	if command.Name == "restic" && len(command.Args) > 0 && command.Args[0] == "backup" {
		return []byte("{\"message_type\":\"summary\",\"snapshot_id\":\"0123456789abcdef\"}\n"), nil
	}
	return []byte("[]"), nil
}

type fakeStore struct {
	finished Run
	pruned   bool
}

type fakeMetrics struct{ names []string }

func (metrics *fakeMetrics) Add(name string, _ float64, _ map[string]string) error {
	metrics.names = append(metrics.names, name)
	return nil
}
func (metrics *fakeMetrics) Observe(name string, _ float64, _ map[string]string) error {
	metrics.names = append(metrics.names, name)
	return nil
}
func (metrics *fakeMetrics) Set(name, _ string, _ float64, _ map[string]string) error {
	metrics.names = append(metrics.names, name)
	return nil
}

func (store *fakeStore) Begin(_ context.Context, trigger, kind string, scheduled time.Time) (Run, error) {
	return Run{ID: "00000000-0000-7000-8000-000000000052", Trigger: trigger, RepositoryKind: kind, State: "running", ScheduledFor: scheduled, StartedAt: scheduled}, nil
}

func (store *fakeStore) Finish(_ context.Context, id, state, snapshotID, errorCode string, files, bytes int64, completed time.Time) (Run, error) {
	store.finished = Run{ID: id, State: state, SnapshotID: snapshotID, ErrorCode: errorCode, FileCount: files, ByteCount: bytes, CompletedAt: &completed}
	return store.finished, nil
}

func (store *fakeStore) PruneHistory(context.Context) error { store.pruned = true; return nil }

func TestDailyScheduleUsesConfiguredTimezone(t *testing.T) {
	schedule, err := ParseDailySchedule("03:00", "Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 7, 1, 0, 30, 0, 0, time.UTC)
	if got, want := schedule.Next(after), time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("next=%s want=%s", got, want)
	}
	if _, err := ParseDailySchedule("25:00", "UTC"); err == nil {
		t.Fatal("invalid schedule was accepted")
	}
}

func TestStagerCreatesPrivateBoundedManifest(t *testing.T) {
	root := t.TempDir()
	cdn := filepath.Join(root, "cdn-source")
	stage := filepath.Join(root, "backup", "source")
	key := filepath.Join(root, "secrets", "master-key")
	if err := os.MkdirAll(filepath.Join(cdn, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cdn, "attachments", "object"), []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("private-master-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "stale"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	stageBefore, err := os.Stat(stage)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	stager, err := NewStager(StagingConfig{Root: stage, CDNRoot: cdn, MasterKeyFile: key, DatabaseURL: "postgres://private", MaxFiles: 10, MaxBytes: 1024}, runner)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := stager.Prepare(context.Background(), time.Date(2026, 9, 8, 3, 0, 0, 0, time.UTC))
	if err != nil || manifest.CDNFiles != 1 || manifest.TotalFiles != 4 || len(manifest.DatabaseSHA256) != 64 {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	encoded, err := os.ReadFile(filepath.Join(stage, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), root) {
		t.Fatalf("manifest leaked private input: %s", encoded)
	}
	if info, err := os.Stat(filepath.Join(stage, "secrets", "master_key")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("staged key permissions=%v err=%v", info, err)
	}
	stageAfter, err := os.Stat(stage)
	if err != nil || !os.SameFile(stageBefore, stageAfter) {
		t.Fatalf("staging root was replaced: before=%v after=%v err=%v", stageBefore, stageAfter, err)
	}
	if _, err := os.Stat(filepath.Join(stage, "stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale staging content remains: %v", err)
	}
	if err := stager.Cleanup(); err != nil {
		t.Fatal(err)
	}
	stageAfterCleanup, err := os.Stat(stage)
	if err != nil || !os.SameFile(stageBefore, stageAfterCleanup) {
		t.Fatalf("cleanup removed staging root: after=%v err=%v", stageAfterCleanup, err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cleanup left staging content: entries=%v err=%v", entries, err)
	}
}

func TestStagerRejectsSymlinksAndLimits(t *testing.T) {
	root := t.TempDir()
	cdn := filepath.Join(root, "cdn")
	key := filepath.Join(root, "key")
	if err := os.MkdirAll(cdn, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(key, filepath.Join(cdn, "link")); err != nil {
		t.Fatal(err)
	}
	stager, err := NewStager(StagingConfig{Root: filepath.Join(root, "stage", "source"), CDNRoot: cdn, MasterKeyFile: key, DatabaseURL: "postgres://db", MaxFiles: 10, MaxBytes: 1024}, &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stager.Prepare(context.Background(), time.Now()); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("symlink error=%v", err)
	}
}

func TestBackupOnlySucceedsAfterVerificationAndRetention(t *testing.T) {
	root := t.TempDir()
	cdn := filepath.Join(root, "cdn")
	key := filepath.Join(root, "key")
	password := filepath.Join(root, "secrets", "restic-password")
	for _, path := range []string{cdn, filepath.Dir(password)} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, value := range map[string]string{key: "master-private", password: "restic-private"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fakeRunner{}
	store := &fakeStore{}
	stager, err := NewStager(StagingConfig{Root: filepath.Join(root, "stage", "source"), CDNRoot: cdn, MasterKeyFile: key, DatabaseURL: "postgres://private-db", MaxFiles: 10, MaxBytes: 1024}, runner)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, stager, runner, ServiceConfig{Repository: filepath.Join(root, "repository"), RepositoryPasswordFile: password})
	if err != nil {
		t.Fatal(err)
	}
	metricRecorder := &fakeMetrics{}
	service.SetMetrics(metricRecorder)
	if _, err := service.RunOnce(context.Background(), "command", time.Now()); err != nil {
		t.Fatal(err)
	}
	if store.finished.State != "succeeded" || store.finished.SnapshotID != "0123456789abcdef" || !store.pruned {
		t.Fatalf("finished=%+v pruned=%v", store.finished, store.pruned)
	}
	want := [][]string{
		{"snapshots", "--json", "--latest", "1"},
		{"backup", "--json", "--quiet", "--host", "mailflow", "--tag", "mailflow", "."},
		{"check", "--read-data-subset=5%"},
		{"forget", "--tag", "mailflow", "--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "12", "--prune"},
	}
	var got [][]string
	for _, command := range runner.commands {
		if command.Name == "restic" {
			got = append(got, command.Args)
		}
		joined := strings.Join(command.Args, " ")
		if strings.Contains(joined, "private") {
			t.Fatalf("command arguments exposed a secret: %q", joined)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands=%v want=%v", got, want)
	}
	if got, want := metricRecorder.names, []string{"mailflow_backup_runs_total", "mailflow_backup_duration_seconds", "mailflow_backup_last_success_unixtime", "mailflow_backup_snapshot_bytes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("metrics=%v want=%v", got, want)
	}
}

func TestBackupFailureIsNeverMarkedSuccessful(t *testing.T) {
	root := t.TempDir()
	cdn, key, password := filepath.Join(root, "cdn"), filepath.Join(root, "key"), filepath.Join(root, "password")
	if err := os.MkdirAll(cdn, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{key, password} {
		if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fakeRunner{failName: "restic"}
	store := &fakeStore{}
	stager, _ := NewStager(StagingConfig{Root: filepath.Join(root, "stage", "source"), CDNRoot: cdn, MasterKeyFile: key, DatabaseURL: "postgres://db", MaxFiles: 10, MaxBytes: 1024}, runner)
	service, _ := NewService(store, stager, runner, ServiceConfig{Repository: filepath.Join(root, "repo"), RepositoryPasswordFile: password})
	if _, err := service.RunOnce(context.Background(), "command", time.Now()); err == nil || store.finished.State != "failed" || store.finished.ErrorCode != "repository_failed" {
		t.Fatalf("finished=%+v err=%v", store.finished, err)
	}
}

func TestRestoreVerifiesAndAppliesOnlyToEmptyTargets(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	dump := filepath.Join(fixture, "database", "mailflow.dump")
	key := filepath.Join(fixture, "secrets", "master_key")
	cdnObject := filepath.Join(fixture, "cdn", "attachments", "object")
	for _, path := range []string{filepath.Dir(dump), filepath.Dir(key), filepath.Dir(cdnObject)} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, value := range map[string]string{dump: "database dump", key: "restored key", cdnObject: "blob"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	digest, _ := fileSHA256(dump)
	manifest := Manifest{Version: ManifestVersion, CreatedAt: time.Now().UTC(), DatabaseSHA256: digest, CDNFiles: 1, CDNBytes: 4, TotalFiles: 4, TotalBytes: 30}
	if err := writeJSONAtomic(filepath.Join(fixture, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(root, "password")
	if err := os.WriteFile(password, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{restoreSource: fixture}
	dummyStager := &Stager{config: StagingConfig{Root: filepath.Join(root, "stage")}, runner: runner}
	service, err := NewService(&fakeStore{}, dummyStager, runner, ServiceConfig{
		Repository: filepath.Join(root, "repository"), RepositoryPasswordFile: password,
		RestoreDatabaseURL: "postgres://empty", RestoreCDNRoot: filepath.Join(root, "restored", "cdn"),
		RestoreMasterKeyOutput: filepath.Join(root, "restored", "master_key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "restore", "snapshot")
	result, err := service.Restore(context.Background(), "0123456789abcdef", target, true)
	if err != nil || result.DatabaseSHA256 != digest {
		t.Fatalf("manifest=%+v err=%v", result, err)
	}
	for path, want := range map[string]string{
		filepath.Join(root, "restored", "cdn", "attachments", "object"): "blob",
		filepath.Join(root, "restored", "master_key"):                   "restored key",
	} {
		value, readErr := os.ReadFile(path)
		if readErr != nil || string(value) != want {
			t.Fatalf("restored %s=%q err=%v", path, value, readErr)
		}
	}
	if _, err := service.Restore(context.Background(), "0123456789abcdef", target, false); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("non-empty restore target error=%v", err)
	}
}
