package mail

import (
	"context"
	"errors"
	stdsync "sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDraftCreatePersistsSanitizedContentAndRelations(t *testing.T) {
	repository, _, userID, accountID := draftFixture(t)
	now := time.Date(2026, 9, 7, 13, 0, 0, 0, time.UTC)
	draft, err := repository.CreateDraft(context.Background(), CreateDraftInput{
		UserID: userID, AccountID: accountID, Now: now,
		Content: DraftContentInput{
			Subject: "Safe fixture", BodyText: "Hello", BodyHTML: `<p>Hello</p><script>alert(1)</script>`,
			Recipients:  []MessageAddressInput{{Role: AddressTo, DisplayName: "Example", Address: "USER@EXAMPLE.TEST"}, {Role: AddressCC, Address: "copy@example.test"}},
			Attachments: []DraftAttachmentInput{{ObjectID: "0123456789abcdef0123456789abcdef", Filename: "note.txt", MediaType: "Text/Plain", SizeBytes: 12}},
		},
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if draft.LocalRevision != 1 || draft.SyncedRevision != 0 || draft.SyncStatus != DraftQueued || !draft.RemoteCheckpointAt.Equal(now.Add(DraftRemoteInterval)) {
		t.Fatalf("created draft state = %+v", draft)
	}
	if draft.BodyHTML != "<p>Hello</p>" || len(draft.Recipients) != 2 || draft.Recipients[0].Address != "USER@EXAMPLE.TEST" || len(draft.Attachments) != 1 || draft.Attachments[0].MediaType != "text/plain" {
		t.Fatalf("created draft content = %+v", draft)
	}
	loaded, err := repository.GetDraft(context.Background(), userID, accountID, draft.ID)
	if err != nil || loaded.ID != draft.ID || len(loaded.Recipients) != 2 || len(loaded.Attachments) != 1 {
		t.Fatalf("loaded draft = %+v, %v", loaded, err)
	}
	if _, err := repository.GetDraft(context.Background(), "0199ed3b-c950-7000-8000-000000000499", accountID, draft.ID); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("cross-owner draft error = %v", err)
	}
}

func TestDraftUpdatesUseOptimisticRevisionAndReplaceRelations(t *testing.T) {
	repository, _, userID, accountID := draftFixture(t)
	created := createDraftFixture(t, repository, userID, accountID)
	input := UpdateDraftInput{
		UserID: userID, AccountID: accountID, DraftID: created.ID, ExpectedRevision: created.LocalRevision, Now: time.Now().UTC(),
		Content: DraftContentInput{Subject: "Updated", BodyText: "Second", BodyHTML: "<p>Second</p>", Recipients: []MessageAddressInput{{Role: AddressBCC, Address: "hidden@example.test"}}},
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	updates := make(chan Draft, 2)
	var wait stdsync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			updated, err := repository.UpdateDraft(context.Background(), input)
			if err == nil {
				updates <- updated
			}
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(updates)
	succeeded, conflicted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrDraftConflict) {
			conflicted++
		} else {
			t.Fatalf("unexpected draft update error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("draft update results = %d succeeded, %d conflicted", succeeded, conflicted)
	}
	updated := <-updates
	if updated.LocalRevision != 2 || updated.Subject != "Updated" || len(updated.Recipients) != 1 || updated.Recipients[0].Role != AddressBCC || len(updated.Attachments) != 0 {
		t.Fatalf("updated draft = %+v", updated)
	}
}

func TestDraftRemoteCheckpointIsIdempotentAndPreservesNewerLocalEdit(t *testing.T) {
	repository, _, userID, accountID := draftFixture(t)
	created := createDraftFixture(t, repository, userID, accountID)
	checkpoint := RemoteDraftCheckpoint{UserID: userID, AccountID: accountID, DraftID: created.ID, LocalRevision: 1, RemoteID: "remote-draft", RemoteRevision: "remote-v1"}
	synced, err := repository.CheckpointRemote(context.Background(), checkpoint)
	if err != nil || synced.SyncStatus != DraftSynced || synced.SyncedRevision != 1 {
		t.Fatalf("checkpoint draft = %+v, %v", synced, err)
	}
	duplicate, err := repository.CheckpointRemote(context.Background(), checkpoint)
	if err != nil || duplicate.SyncedRevision != 1 {
		t.Fatalf("duplicate checkpoint = %+v, %v", duplicate, err)
	}
	updated, err := repository.UpdateDraft(context.Background(), UpdateDraftInput{
		UserID: userID, AccountID: accountID, DraftID: created.ID, ExpectedRevision: 1, Now: time.Now().UTC(),
		Content: DraftContentInput{Subject: "Newer", BodyText: "Newer", BodyHTML: "<p>Newer</p>"},
	})
	if err != nil || updated.LocalRevision != 2 || updated.SyncStatus != DraftQueued {
		t.Fatalf("newer draft = %+v, %v", updated, err)
	}
	duplicate, err = repository.CheckpointRemote(context.Background(), checkpoint)
	if err != nil || duplicate.LocalRevision != 2 || duplicate.SyncStatus != DraftQueued {
		t.Fatalf("old idempotent checkpoint overwrote edit = %+v, %v", duplicate, err)
	}
	checkpoint.RemoteRevision = "remote-v2"
	if _, err := repository.CheckpointRemote(context.Background(), checkpoint); !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("stale checkpoint error = %v", err)
	}
	checkpoint.LocalRevision = 2
	resynced, err := repository.CheckpointRemote(context.Background(), checkpoint)
	if err != nil || resynced.SyncedRevision != 2 || resynced.SyncStatus != DraftSynced {
		t.Fatalf("current checkpoint = %+v, %v", resynced, err)
	}
	discarded, err := repository.DiscardDraft(context.Background(), userID, accountID, created.ID)
	if err != nil || discarded.SyncStatus != DraftDiscarded || discarded.DiscardedAt == nil {
		t.Fatalf("discard draft = %+v, %v", discarded, err)
	}
	if _, err := repository.UpdateDraft(context.Background(), UpdateDraftInput{UserID: userID, AccountID: accountID, DraftID: created.ID, ExpectedRevision: 2, Now: time.Now().UTC(), Content: DraftContentInput{}}); !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("update discarded draft error = %v", err)
	}
}

func TestDraftSchedulingAndAttachmentValidation(t *testing.T) {
	now := time.Date(2026, 9, 7, 14, 0, 0, 0, time.UTC)
	schedule, err := ScheduleDraftAutosave(now)
	if err != nil || !schedule.LocalSaveAt.Equal(now.Add(2*time.Second)) || !schedule.RemoteCheckpointAt.Equal(now.Add(15*time.Second)) {
		t.Fatalf("draft schedule = %+v, %v", schedule, err)
	}
	repository, _, userID, accountID := draftFixture(t)
	_, err = repository.CreateDraft(context.Background(), CreateDraftInput{
		UserID: userID, AccountID: accountID, Now: now,
		Content: DraftContentInput{Attachments: []DraftAttachmentInput{{ObjectID: "../../private", MediaType: "text/plain"}}},
	})
	if !errors.Is(err, ErrInvalidDraft) {
		t.Fatalf("invalid attachment error = %v", err)
	}
}

func createDraftFixture(t *testing.T, repository *DraftRepositoryStore, userID, accountID string) Draft {
	t.Helper()
	draft, err := repository.CreateDraft(context.Background(), CreateDraftInput{
		UserID: userID, AccountID: accountID, Now: time.Now().UTC(),
		Content: DraftContentInput{Subject: "Initial", BodyText: "Initial", BodyHTML: "<p>Initial</p>", Recipients: []MessageAddressInput{{Role: AddressTo, Address: "recipient@example.test"}}, Attachments: []DraftAttachmentInput{{ObjectID: "abcdef0123456789abcdef0123456789", MediaType: "text/plain", SizeBytes: 5}}},
	})
	if err != nil {
		t.Fatalf("create draft fixture: %v", err)
	}
	return draft
}

func draftFixture(t *testing.T) (*DraftRepositoryStore, *pgxpool.Pool, string, string) {
	t.Helper()
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("migrate draft database: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open draft database: %v", err)
	}
	t.Cleanup(pool.Close)
	userID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000033")
	accountID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000133")
	if _, err := pool.Exec(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "draft-owner@example.test", "Owner"); err != nil {
		t.Fatalf("create draft owner: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'google', 'draft-owner', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("create draft account: %v", err)
	}
	return NewDraftRepository(pool), pool, userID.String(), accountID.String()
}
