package testkit

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	redis "github.com/redis/go-redis/v9"
)

const serviceTimeout = 10 * time.Second

// PostgresDatabase creates an isolated database on the configured test server.
// The base database must end in _test so a production URL cannot be used by
// accident. Cleanup forcibly removes all test connections and the database.
func PostgresDatabase(t testing.TB) string {
	t.Helper()
	databaseURL := os.Getenv("MAILFLOW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MAILFLOW_TEST_DATABASE_URL is not set; run make integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to the PostgreSQL test service: %v", err)
	}

	var baseDatabase string
	if err := admin.QueryRow(ctx, "select current_database()").Scan(&baseDatabase); err != nil {
		admin.Close(context.Background())
		t.Fatalf("read the PostgreSQL test database name: %v", err)
	}
	if !strings.HasSuffix(baseDatabase, "_test") {
		admin.Close(context.Background())
		t.Fatalf("refusing PostgreSQL database %q: base name must end in _test", baseDatabase)
	}
	databaseName := "mailflow_it_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := admin.Exec(ctx, "create database "+identifier); err != nil {
		admin.Close(context.Background())
		t.Fatalf("create isolated PostgreSQL test database: %v", err)
	}

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		_, _ = admin.Exec(context.Background(), "drop database "+identifier+" with (force)")
		admin.Close(context.Background())
		t.Fatalf("parse PostgreSQL test configuration: %v", err)
	}
	parsed.Path = "/" + databaseName
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), serviceTimeout)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "drop database "+identifier+" with (force)"); err != nil {
			t.Errorf("remove isolated PostgreSQL test database: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL test service connection: %v", err)
		}
	})
	return parsed.String()
}

// Redis creates a client and unique key prefix on the configured test server.
// Every key under that prefix is removed when the test ends.
func Redis(t testing.TB) (*redis.Client, string) {
	t.Helper()
	address := os.Getenv("MAILFLOW_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("MAILFLOW_TEST_REDIS_ADDRESS is not set; run make integration")
	}
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: serviceTimeout})
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("connect to the Redis test service: %v", err)
	}
	prefix := "mailflow-it:" + uuid.NewString()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), serviceTimeout)
		defer cleanupCancel()
		iterator := client.Scan(cleanupCtx, 0, prefix+":*", 100).Iterator()
		keys := make([]string, 0, 100)
		for iterator.Next(cleanupCtx) {
			keys = append(keys, iterator.Val())
		}
		if err := iterator.Err(); err != nil {
			t.Errorf("scan isolated Redis test keys: %v", err)
		} else if len(keys) > 0 {
			if err := client.Unlink(cleanupCtx, keys...).Err(); err != nil {
				t.Errorf("remove isolated Redis test keys: %v", err)
			}
		}
		if err := client.Close(); err != nil {
			t.Errorf("close Redis test service connection: %v", err)
		}
	})
	return client, prefix
}
