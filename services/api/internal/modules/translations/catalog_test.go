package translations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

type recordingPublisher struct {
	userID    string
	eventType string
	payload   json.RawMessage
}

func (publisher *recordingPublisher) Publish(_ context.Context, userID, eventType string, payload json.RawMessage) (events.Envelope, error) {
	publisher.userID = userID
	publisher.eventType = eventType
	publisher.payload = payload
	return events.Envelope{Version: events.EnvelopeVersion, Type: eventType, Payload: payload}, nil
}

func TestBuiltinCatalogIsCompleteWithEnglishFallback(t *testing.T) {
	catalog := NewCatalog()
	english, err := catalog.Get(context.Background(), "en")
	if err != nil || english.DefaultLocale != "en" || len(english.Messages) != len(builtinCatalogs["en"]) {
		t.Fatalf("english=%+v err=%v", english, err)
	}
	spanish, err := catalog.Get(context.Background(), "es")
	if err != nil || len(spanish.Messages) != len(english.Messages) || len(spanish.MissingKeys) != 0 {
		t.Fatalf("spanish messages=%d missing=%v err=%v", len(spanish.Messages), spanish.MissingKeys, err)
	}
	fallback, err := catalog.Get(context.Background(), "fr")
	if err != nil || fallback.Locale != "en" || fallback.Messages["inbox"] != "Inbox" {
		t.Fatalf("fallback=%+v err=%v", fallback, err)
	}
}

func TestICUValidationRejectsBrokenOrMismatchedArguments(t *testing.T) {
	valid := []string{
		"Hello {name}",
		"{count, plural, =0 {None} one {One} other {{count} messages}}",
		"{gender, select, female {Her inbox} male {His inbox} other {Their inbox}}",
	}
	for _, message := range valid {
		if _, ok := icuArguments(message); !ok {
			t.Fatalf("valid ICU rejected: %s", message)
		}
	}
	for _, message := range []string{"Hello {", "{count, plural, one {One}}", "{,number}"} {
		if _, ok := icuArguments(message); ok {
			t.Fatalf("invalid ICU accepted: %s", message)
		}
	}
}

func TestPersistentCatalogVersionsValidatesPublishesAndRetains(t *testing.T) {
	ctx := context.Background()
	databaseURL := testkit.PostgresDatabase(t)
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	const ownerID = "00000000-0000-7000-8000-000000000050"
	if _, err := pool.Exec(ctx, `insert into users (id,email,name,locale) values ($1,'owner@example.test','owner','en')`, ownerID); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	catalog, err := NewPersistentCatalog(ctx, pool, publisher)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := catalog.Export(ctx)
	if err != nil || initial.Revision < 1 || initial.Diagnostics.blocking() || len(initial.Diagnostics.MissingSpanish) != 0 {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	changedSubject := "Topic"
	stalePreview, err := catalog.Validate(ctx, UpdateRequest{ExpectedRevision: initial.Revision, Changes: []Change{{Locale: "en", Key: "subject", Value: &changedSubject}}})
	if err != nil || len(stalePreview.Diagnostics.StaleSpanish) != 1 || stalePreview.Diagnostics.StaleSpanish[0] != "subject" {
		t.Fatalf("stale preview=%+v err=%v", stalePreview.Diagnostics, err)
	}

	result, err := catalog.Update(ctx, ownerID, UpdateRequest{ExpectedRevision: initial.Revision, Changes: []Change{{Locale: "es", Key: "inbox", Value: nil}}})
	if err != nil || result.Revision != initial.Revision+1 || !result.EventPublished || result.Catalogs["es"]["inbox"].Value != "" {
		t.Fatalf("fallback result=%+v err=%v", result, err)
	}
	if publisher.userID != ownerID || publisher.eventType != "translations.changed" || string(publisher.payload) != `{"revision":2}` {
		t.Fatalf("event user=%s type=%s payload=%s", publisher.userID, publisher.eventType, publisher.payload)
	}
	changedStarred := "Favorites"
	staleSpanish, err := catalog.Validate(ctx, UpdateRequest{ExpectedRevision: result.Revision, Changes: []Change{{Locale: "en", Key: "starred", Value: &changedStarred}}})
	if err != nil || len(staleSpanish.Diagnostics.StaleSpanish) != 1 || staleSpanish.Diagnostics.StaleSpanish[0] != "starred" {
		t.Fatalf("stale Spanish diagnostics=%+v err=%v", staleSpanish.Diagnostics, err)
	}
	if _, err := catalog.Update(ctx, ownerID, UpdateRequest{ExpectedRevision: result.Revision, Changes: []Change{{Locale: "en", Key: "starred", Value: &changedStarred}}}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("stale Spanish activation err=%v", err)
	}
	spanish, err := catalog.Get(ctx, "es")
	if err != nil || spanish.Messages["inbox"] != "Inbox" || len(spanish.MissingKeys) != 1 || spanish.MissingKeys[0] != "inbox" {
		t.Fatalf("spanish fallback=%+v err=%v", spanish, err)
	}

	englishValue := "Mailbox"
	stale, err := catalog.Validate(ctx, UpdateRequest{ExpectedRevision: result.Revision, Changes: []Change{{Locale: "en", Key: "inbox", Value: &englishValue}}})
	if err != nil || len(stale.Diagnostics.StaleSpanish) != 0 {
		t.Fatalf("removed Spanish override should not become stale: %+v err=%v", stale.Diagnostics, err)
	}
	spanishValue := "Buzón"
	updated, err := catalog.Update(ctx, ownerID, UpdateRequest{ExpectedRevision: result.Revision, Changes: []Change{
		{Locale: "en", Key: "inbox", Value: &englishValue},
		{Locale: "es", Key: "inbox", Value: &spanishValue, SourceHash: sourceHash(englishValue)},
	}})
	if err != nil || updated.Catalogs["en"]["inbox"].Value != englishValue || updated.Catalogs["es"]["inbox"].Value != spanishValue {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}

	badEnglish := "Hello {name"
	invalid, err := catalog.Validate(ctx, UpdateRequest{ExpectedRevision: updated.Revision, Changes: []Change{{Locale: "en", Key: "inbox", Value: &badEnglish}}})
	if err != nil || len(invalid.Diagnostics.InvalidICU) == 0 {
		t.Fatalf("invalid diagnostics=%+v err=%v", invalid.Diagnostics, err)
	}
	private := "Contact owner@example.test"
	privateResult, err := catalog.Update(ctx, ownerID, UpdateRequest{ExpectedRevision: updated.Revision, Changes: []Change{{Locale: "en", Key: "inbox", Value: &private}}})
	if !errors.Is(err, ErrInvalidCatalog) || len(privateResult.Diagnostics.PrivateValues) == 0 {
		t.Fatalf("private result=%+v err=%v", privateResult, err)
	}
	missing, err := catalog.Validate(ctx, UpdateRequest{ExpectedRevision: updated.Revision, Changes: []Change{{Locale: "en", Key: "inbox", Value: nil}}})
	if err != nil || len(missing.Diagnostics.MissingEnglish) != 1 || missing.Diagnostics.MissingEnglish[0] != "inbox" {
		t.Fatalf("missing English diagnostics=%+v err=%v", missing.Diagnostics, err)
	}
	if _, err := catalog.Update(ctx, ownerID, UpdateRequest{ExpectedRevision: initial.Revision, Changes: []Change{{Locale: "es", Key: "inbox", Value: &spanishValue, SourceHash: sourceHash(englishValue)}}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision err=%v", err)
	}

	for range RetainedRevisions + 5 {
		if _, err := pool.Exec(ctx, `insert into translation_revisions (message_count,created_at) values (1,$1)`, time.Now().UTC().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	finalValue := "Correo"
	final, err := catalog.Update(ctx, ownerID, UpdateRequest{ExpectedRevision: updated.Revision, Changes: []Change{{Locale: "es", Key: "inbox", Value: &finalValue, SourceHash: sourceHash(englishValue)}}})
	if err != nil {
		t.Fatal(err)
	}
	var revisionCount int
	if err := pool.QueryRow(ctx, `select count(*) from translation_revisions`).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != RetainedRevisions || final.Revision <= updated.Revision {
		t.Fatalf("revision count=%d final=%d", revisionCount, final.Revision)
	}

	second, err := NewPersistentCatalog(ctx, pool, publisher)
	if err != nil {
		t.Fatal(err)
	}
	secondSpanish, err := second.Get(ctx, "es")
	if err != nil || secondSpanish.Revision != final.Revision || secondSpanish.Messages["inbox"] != finalValue {
		t.Fatalf("refreshed=%+v err=%v", secondSpanish, err)
	}
}
