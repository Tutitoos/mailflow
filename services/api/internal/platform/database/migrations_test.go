package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/db/migrations"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestAuthIdentityMigrationPreservesFoundationUser(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 1); err != nil {
		t.Fatalf("apply foundation migration: %v", err)
	}

	id := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb776")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, locale) values ($1, $2, $3)", id, "owner@example.test", "es"); err != nil {
		t.Fatalf("insert foundation user: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply identity migration: %v", err)
	}
	if err := Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}

	var email, locale, name string
	if err := database.QueryRowContext(ctx, "select email, locale, name from users where id = $1", id).Scan(&email, &locale, &name); err != nil {
		t.Fatalf("load migrated user: %v", err)
	}
	if email != "owner@example.test" || locale != "es" || name != "owner" {
		t.Fatalf("migrated user = email %q, locale %q, name %q", email, locale, name)
	}

	for _, table := range []string{"auth_sessions", "auth_accounts", "auth_verifications", "auth_passkeys", "auth_jwks"} {
		var exists bool
		if err := database.QueryRowContext(ctx, "select to_regclass($1) is not null", table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	var legacyTableExists bool
	if err := database.QueryRowContext(ctx, `select to_regclass('"user"') is not null`).Scan(&legacyTableExists); err != nil {
		t.Fatalf("check legacy user table: %v", err)
	}
	if legacyTableExists {
		t.Fatal("legacy Better Auth user table unexpectedly exists")
	}
	if _, err := database.ExecContext(ctx, "insert into users (email, name) values ($1, $2)", "second@example.test", "Second"); err == nil {
		t.Fatal("single-user database constraint accepted a second profile")
	}
}

func TestMailboxMigrationPreservesExistingUnreadCounters(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 4); err != nil {
		t.Fatalf("apply account migrations: %v", err)
	}

	userID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb727")
	accountID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb728")
	mailboxID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb729")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "migration-owner@example.test", "Owner"); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		insert into accounts (
			id, user_id, provider, remote_id, display_name,
			encrypted_credentials, credential_nonce, capabilities
		) values ($1, $2, 'google', 'migration-owner', 'Personal', $3, $4, '{}')
	`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		insert into mailboxes (id, account_id, remote_id, name, role, unread_count)
		values ($1, $2, 'INBOX', 'Inbox', 'inbox', 7)
	`, mailboxID, accountID); err != nil {
		t.Fatalf("insert legacy mailbox: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply mailbox migration: %v", err)
	}

	var remoteName string
	var total, unread int
	if err := database.QueryRowContext(ctx, `
		select remote_name, total_count, unread_count
		from mailboxes where id = $1
	`, mailboxID).Scan(&remoteName, &total, &unread); err != nil {
		t.Fatalf("load migrated mailbox: %v", err)
	}
	if remoteName != "Inbox" || total != 7 || unread != 7 {
		t.Fatalf("migrated mailbox = name %q, total %d, unread %d", remoteName, total, unread)
	}
}

func TestThreadMigrationPreservesExistingThreadState(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 5); err != nil {
		t.Fatalf("apply mailbox migrations: %v", err)
	}

	userID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb728")
	accountID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb729")
	threadID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb730")
	messageID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb731")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "thread-migration@example.test", "Owner"); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		insert into accounts (
			id, user_id, provider, remote_id, display_name,
			encrypted_credentials, credential_nonce, capabilities
		) values ($1, $2, 'google', 'thread-migration', 'Personal', $3, $4, '{}')
	`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		insert into threads (
			id, account_id, remote_id, last_message_at, is_read, is_starred, category
		) values ($1, $2, 'legacy-thread', '2026-09-07T16:00:00Z', true, true, 'primary')
	`, threadID, accountID); err != nil {
		t.Fatalf("insert legacy thread: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		insert into messages (
			id, thread_id, remote_id, sender, recipients, sent_at
		) values ($1, $2, 'legacy-message', '{}', '[]', '2026-09-07T16:00:00Z')
	`, messageID, threadID); err != nil {
		t.Fatalf("insert legacy message: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply thread migration: %v", err)
	}

	var messageAccount uuid.UUID
	var messageRead, messageStarred bool
	if err := database.QueryRowContext(ctx, `
		select account_id, is_read, is_starred
		from messages where id = $1
	`, messageID).Scan(&messageAccount, &messageRead, &messageStarred); err != nil {
		t.Fatalf("load migrated message: %v", err)
	}
	if messageAccount != accountID || !messageRead || !messageStarred {
		t.Fatalf("migrated message = account %s, read %v, starred %v", messageAccount, messageRead, messageStarred)
	}
	var count, unread int
	var threadRead, threadStarred bool
	if err := database.QueryRowContext(ctx, `
		select message_count, unread_count, is_read, is_starred
		from threads where id = $1
	`, threadID).Scan(&count, &unread, &threadRead, &threadStarred); err != nil {
		t.Fatalf("load migrated thread: %v", err)
	}
	if count != 1 || unread != 0 || !threadRead || !threadStarred {
		t.Fatalf("migrated thread = count %d, unread %d, read %v, starred %v", count, unread, threadRead, threadStarred)
	}
}

func TestMessageContentMigrationPreservesExistingBodies(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 6); err != nil {
		t.Fatalf("apply thread migrations: %v", err)
	}

	userID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb738")
	accountID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb739")
	threadID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb740")
	messageID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb741")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "content-migration@example.test", "Owner"); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'google', 'content-migration', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into threads (id, account_id, remote_id, last_message_at) values ($1, $2, 'content-thread', '2026-09-07T16:00:00Z')`, threadID, accountID); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into messages (id, thread_id, account_id, remote_id, sender, recipients, subject, body_text, body_html_sanitized, sent_at) values ($1, $2, $3, 'content-message', '{}', '[]', 'Existing', 'Existing text', '<p>Existing text</p>', '2026-09-07T16:00:00Z')`, messageID, threadID, accountID); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply content migration: %v", err)
	}
	var subject, bodyText, bodyHTML string
	var replyCount int
	if err := database.QueryRowContext(ctx, `select subject, body_text, body_html_sanitized, cardinality(in_reply_to) from messages where id = $1`, messageID).Scan(&subject, &bodyText, &bodyHTML, &replyCount); err != nil {
		t.Fatalf("load migrated content: %v", err)
	}
	if subject != "Existing" || bodyText != "Existing text" || bodyHTML != "<p>Existing text</p>" || replyCount != 0 {
		t.Fatalf("migrated content = %q, %q, %q, replies %d", subject, bodyText, bodyHTML, replyCount)
	}
	if err := goose.DownToContext(ctx, database, ".", 6); err != nil {
		t.Fatalf("roll back content migration: %v", err)
	}
	if err := database.QueryRowContext(ctx, `select subject, body_text, body_html_sanitized from messages where id = $1`, messageID).Scan(&subject, &bodyText, &bodyHTML); err != nil {
		t.Fatalf("load rolled-back content: %v", err)
	}
	if subject != "Existing" || bodyText != "Existing text" || bodyHTML != "<p>Existing text</p>" {
		t.Fatalf("rolled-back content = %q, %q, %q", subject, bodyText, bodyHTML)
	}
}

func TestMailSearchMigrationRebuildsAndRollsBackExistingIndex(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 7); err != nil {
		t.Fatalf("apply content migrations: %v", err)
	}
	userID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb748")
	accountID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb749")
	threadID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb750")
	messageID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb751")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "search-migration@example.test", "Owner"); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'google', 'search-migration', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into threads (id, account_id, remote_id, last_message_at) values ($1, $2, 'search-thread', '2026-09-07T16:00:00Z')`, threadID, accountID); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into messages (id, thread_id, account_id, remote_id, sender, recipients, subject, body_text, sent_at) values ($1, $2, $3, 'search-message', '{}', '[]', 'Weighted subject', 'needle body', '2026-09-07T16:00:00Z')`, messageID, threadID, accountID); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply search migration: %v", err)
	}
	var matches int
	if err := database.QueryRowContext(ctx, `select count(*) from messages where search_vector @@ websearch_to_tsquery('simple', 'needle')`).Scan(&matches); err != nil || matches != 1 {
		t.Fatalf("rebuilt search index: matches=%d error=%v", matches, err)
	}
	if err := goose.DownToContext(ctx, database, ".", 7); err != nil {
		t.Fatalf("roll back search migration: %v", err)
	}
	if err := database.QueryRowContext(ctx, `select count(*) from messages where search_vector @@ websearch_to_tsquery('simple', 'needle')`).Scan(&matches); err != nil || matches != 1 {
		t.Fatalf("rolled-back search index: matches=%d error=%v", matches, err)
	}
}

func TestSyncStateMigrationPreservesAndRollsBackExistingCursor(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 8); err != nil {
		t.Fatalf("apply search migrations: %v", err)
	}

	userID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb758")
	accountID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb759")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "sync-migration@example.test", "Owner"); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'google', 'sync-migration', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into sync_cursors (account_id, kind, cursor) values ($1, 'gmail', '{"historyId":"42"}')`, accountID); err != nil {
		t.Fatalf("insert legacy cursor: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply sync migration: %v", err)
	}

	var kind, state, cursor string
	var checkpoint, version int64
	if err := database.QueryRowContext(ctx, `select kind, state, cursor::text, checkpoint, version from sync_cursors where account_id = $1`, accountID).Scan(&kind, &state, &cursor, &checkpoint, &version); err != nil {
		t.Fatalf("load migrated cursor: %v", err)
	}
	if kind != "google_history" || state != "active" || cursor != `{"historyId": "42"}` || checkpoint != 0 || version != 1 {
		t.Fatalf("migrated cursor = kind %q, state %q, value %q, checkpoint %d, version %d", kind, state, cursor, checkpoint, version)
	}
	if err := goose.DownToContext(ctx, database, ".", 8); err != nil {
		t.Fatalf("roll back sync migration: %v", err)
	}
	if err := database.QueryRowContext(ctx, `select kind, cursor::text from sync_cursors where account_id = $1`, accountID).Scan(&kind, &cursor); err != nil {
		t.Fatalf("load rolled-back cursor: %v", err)
	}
	if kind != "gmail" || cursor != `{"historyId": "42"}` {
		t.Fatalf("rolled-back cursor = kind %q, value %q", kind, cursor)
	}
}

func TestPendingActionMigrationPreservesAndRollsBackExistingAction(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 9); err != nil {
		t.Fatalf("apply sync migrations: %v", err)
	}
	userID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb768")
	accountID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb769")
	actionID := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb770")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "action-migration@example.test", "Owner"); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'google', 'action-migration', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := database.ExecContext(ctx, `insert into pending_actions (id, account_id, idempotency_key, action, payload) values ($1, $2, 'legacy-request-01', 'mark_read', '{"read":true}')`, actionID, accountID); err != nil {
		t.Fatalf("insert legacy action: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply pending-action migration: %v", err)
	}
	var kind, targetKind, desired string
	var targetID uuid.UUID
	if err := database.QueryRowContext(ctx, `select kind, target_kind, target_id, desired_state::text from pending_actions where id = $1`, actionID).Scan(&kind, &targetKind, &targetID, &desired); err != nil {
		t.Fatalf("load migrated action: %v", err)
	}
	if kind != "mark_read" || targetKind != "thread" || targetID != actionID || desired != `{"read": true}` {
		t.Fatalf("migrated action = kind %q, target %q/%s, state %q", kind, targetKind, targetID, desired)
	}
	if err := goose.DownToContext(ctx, database, ".", 9); err != nil {
		t.Fatalf("roll back pending-action migration: %v", err)
	}
	if err := database.QueryRowContext(ctx, `select action, payload::text from pending_actions where id = $1`, actionID).Scan(&kind, &desired); err != nil {
		t.Fatalf("load rolled-back action: %v", err)
	}
	if kind != "mark_read" || desired != `{"read": true}` {
		t.Fatalf("rolled-back action = kind %q, state %q", kind, desired)
	}
}

func TestDraftMigrationCreatesRelationsAndRollsBackCleanly(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply draft migration: %v", err)
	}
	for _, table := range []string{"drafts", "draft_recipients", "draft_attachments"} {
		var exists bool
		if err := database.QueryRowContext(ctx, "select to_regclass($1) is not null", table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	if err := goose.DownToContext(ctx, database, ".", 10); err != nil {
		t.Fatalf("roll back draft migration: %v", err)
	}
	for _, table := range []string{"drafts", "draft_recipients", "draft_attachments"} {
		var exists bool
		if err := database.QueryRowContext(ctx, "select to_regclass($1) is not null", table).Scan(&exists); err != nil || exists {
			t.Fatalf("rolled-back table %s exists = %v, error = %v", table, exists, err)
		}
	}
}

func TestCDNMigrationCreatesMetadataAndRollsBackCleanly(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply CDN migration: %v", err)
	}
	var tableExists, columnExists bool
	if err := database.QueryRowContext(ctx, `select to_regclass('cdn_objects') is not null`).Scan(&tableExists); err != nil || !tableExists {
		t.Fatalf("CDN metadata table exists = %v, error = %v", tableExists, err)
	}
	if err := database.QueryRowContext(ctx, `select exists(select 1 from information_schema.columns where table_name='message_attachments' and column_name='cached_object_id')`).Scan(&columnExists); err != nil || !columnExists {
		t.Fatalf("attachment cache column exists = %v, error = %v", columnExists, err)
	}
	if err := goose.DownToContext(ctx, database, ".", 11); err != nil {
		t.Fatalf("roll back CDN migration: %v", err)
	}
	if err := database.QueryRowContext(ctx, `select to_regclass('cdn_objects') is not null`).Scan(&tableExists); err != nil || tableExists {
		t.Fatalf("rolled-back CDN table exists = %v, error = %v", tableExists, err)
	}
}
