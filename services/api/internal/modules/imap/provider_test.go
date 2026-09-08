package imap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

type providerFixtureStore struct {
	folders   []FolderState
	locations []MessageLocation
	moves     [][2]MessageLocation
}

func (store *providerFixtureStore) Folders(context.Context, string, string) ([]FolderState, error) {
	return append([]FolderState(nil), store.folders...), nil
}
func (store *providerFixtureStore) Locations(context.Context, string, string, []string) ([]MessageLocation, error) {
	return append([]MessageLocation(nil), store.locations...), nil
}
func (store *providerFixtureStore) MoveLocation(_ context.Context, _, _ string, source, destination MessageLocation) error {
	store.moves = append(store.moves, [2]MessageLocation{source, destination})
	return nil
}

type providerFixtureProtocol struct {
	fetches       []FetchRequest
	fetchResults  []FetchResult
	fetchErr      error
	flagLocations []MessageLocation
	addedFlags    [][]string
	removedFlags  [][]string
	moved         []MessageLocation
	moveResult    MoveResult
	appended      [][]byte
	appendResult  AppendResult
	deleted       []MessageLocation
	raw           []byte
	smtpCalls     int
	smtpErr       error
}

func (protocol *providerFixtureProtocol) Fetch(_ context.Context, _ storedCredentials, request FetchRequest) (FetchResult, error) {
	protocol.fetches = append(protocol.fetches, request)
	if protocol.fetchErr != nil {
		return FetchResult{}, protocol.fetchErr
	}
	if len(protocol.fetchResults) == 0 {
		return FetchResult{NextUID: request.FromUID}, nil
	}
	result := protocol.fetchResults[0]
	protocol.fetchResults = protocol.fetchResults[1:]
	return result, nil
}
func (protocol *providerFixtureProtocol) SetFlags(_ context.Context, _ storedCredentials, location MessageLocation, add, remove []string) error {
	protocol.flagLocations = append(protocol.flagLocations, location)
	protocol.addedFlags = append(protocol.addedFlags, append([]string(nil), add...))
	protocol.removedFlags = append(protocol.removedFlags, append([]string(nil), remove...))
	return nil
}
func (protocol *providerFixtureProtocol) Move(_ context.Context, _ storedCredentials, location MessageLocation, _ FolderState, _ map[string]bool) (MoveResult, error) {
	protocol.moved = append(protocol.moved, location)
	return protocol.moveResult, nil
}
func (protocol *providerFixtureProtocol) Append(_ context.Context, _ storedCredentials, _ FolderState, raw []byte, _ []string) (AppendResult, error) {
	protocol.appended = append(protocol.appended, append([]byte(nil), raw...))
	return protocol.appendResult, nil
}
func (protocol *providerFixtureProtocol) MarkDeleted(_ context.Context, _ storedCredentials, location MessageLocation) error {
	protocol.deleted = append(protocol.deleted, location)
	return nil
}
func (protocol *providerFixtureProtocol) RawMessage(context.Context, storedCredentials, MessageLocation) ([]byte, error) {
	return append([]byte(nil), protocol.raw...), nil
}
func (protocol *providerFixtureProtocol) SendSMTP(context.Context, storedCredentials, []byte) error {
	protocol.smtpCalls++
	return protocol.smtpErr
}

func TestProviderBackfillNormalizesStableThreadsAndMutableLocations(t *testing.T) {
	stamp := time.Date(2026, time.September, 8, 10, 0, 0, 0, time.UTC)
	root := fixtureMIME("root@example.test", "", "Root", "body")
	reply := fixtureMIME("reply@example.test", "root@example.test", "Re: Root", "reply")
	store := &providerFixtureStore{folders: []FolderState{providerFolder("inbox", "INBOX", mail.MailboxInbox, 9, 20)}}
	protocol := &providerFixtureProtocol{fetchResults: []FetchResult{{Messages: []FetchedMessage{
		{UID: 4, UIDValidity: 9, SentAt: stamp, Raw: root},
		{UID: 7, UIDValidity: 9, SentAt: stamp.Add(time.Minute), Flags: []string{"\\Seen", "\\Flagged"}, Raw: reply},
	}, NextUID: 8, SnapshotUIDs: []int64{4, 7}}}}
	provider := newProviderFixture(t, store, protocol)
	page, err := provider.Backfill(context.Background(), mail.SyncCursor{}, nil, nil, 100)
	if err != nil || len(page.Messages) != 2 || page.HasMore || len(page.LocationSnapshots) != 1 || len(page.LocationSnapshots[0].PresentUIDs) != 2 {
		t.Fatalf("backfill page=%+v error=%v", page, err)
	}
	if page.Messages[0].ThreadID != page.Messages[1].ThreadID || page.Messages[0].RemoteID == page.Messages[1].RemoteID {
		t.Fatalf("stable identities = %q/%q threads=%q/%q", page.Messages[0].RemoteID, page.Messages[1].RemoteID, page.Messages[0].ThreadID, page.Messages[1].ThreadID)
	}
	if page.Messages[1].Locations[0].UID != 7 || page.Messages[1].Locations[0].MailboxID != "inbox" || !page.Messages[1].IsRead || !page.Messages[1].IsStarred {
		t.Fatalf("normalized reply = %+v", page.Messages[1])
	}
	// The same RFC message at a new UID keeps its domain identity.
	protocol.fetchResults = []FetchResult{{Messages: []FetchedMessage{{UID: 12, UIDValidity: 9, SentAt: stamp, Raw: root}}, NextUID: 13}}
	page, err = provider.Backfill(context.Background(), mail.SyncCursor{}, nil, nil, 100)
	if err != nil || page.Messages[0].RemoteID != stableMessageID(root, "root@example.test") || page.Messages[0].Locations[0].UID != 12 {
		t.Fatalf("moved identity = %+v error=%v", page.Messages, err)
	}
}

func TestProviderMalformedReferencesFallBackToMessageIdentity(t *testing.T) {
	raw := []byte("From: sender@example.test\r\nTo: owner@example.test\r\nMessage-ID: <standalone@example.test>\r\nReferences: malformed value\r\nSubject: Fixture\r\n\r\nbody")
	store := &providerFixtureStore{folders: []FolderState{providerFolder("inbox", "INBOX", mail.MailboxInbox, 2, 3)}}
	protocol := &providerFixtureProtocol{fetchResults: []FetchResult{{Messages: []FetchedMessage{{UID: 1, UIDValidity: 2, SentAt: time.Now().UTC(), Raw: raw}}, NextUID: 2}}}
	provider := newProviderFixture(t, store, protocol)
	page, err := provider.Backfill(context.Background(), mail.SyncCursor{}, nil, nil, 10)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("malformed reference page=%+v error=%v", page, err)
	}
	expected := stableIdentifier("thread", "standalone@example.test")
	if page.Messages[0].ThreadID != expected || len(page.Messages[0].Content.References) != 0 {
		t.Fatalf("fallback thread=%q references=%v", page.Messages[0].ThreadID, page.Messages[0].Content.References)
	}
}

func TestProviderIncrementalCursorRejectsUIDValidityChanges(t *testing.T) {
	store := &providerFixtureStore{folders: []FolderState{providerFolder("inbox", "INBOX", mail.MailboxInbox, 11, 42)}}
	protocol := &providerFixtureProtocol{fetchErr: ErrUIDValidityChanged}
	provider := newProviderFixture(t, store, protocol)
	profile, err := provider.Profile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Changes(context.Background(), profile.History); !errors.Is(err, ErrUIDValidityChanged) {
		t.Fatalf("cursor invalidation error = %v", err)
	}
	var cursor imapCursor
	if json.Unmarshal(profile.History.Value, &cursor) != nil || cursor.Folders[0].NextUID != 42 {
		t.Fatalf("profile cursor = %s", profile.History.Value)
	}
}

func TestProviderActionsUseAllStableLocationsAndPersistMoveUID(t *testing.T) {
	store := &providerFixtureStore{
		folders: []FolderState{
			providerFolder("inbox", "INBOX", mail.MailboxInbox, 5, 10),
			providerFolder("archive", "Archive", mail.MailboxArchive, 8, 20),
		},
		locations: []MessageLocation{{MessageRemoteID: "message-1", MailboxRemoteID: "inbox", WireName: "INBOX", UIDValidity: 5, UID: 7}},
	}
	protocol := &providerFixtureProtocol{moveResult: MoveResult{UIDValidity: 8, UID: 21}}
	provider := newProviderFixture(t, store, protocol)
	if err := provider.Apply(context.Background(), mail.RemoteAction{Kind: string(mail.ActionMarkRead), TargetIDs: []string{"message-1"}}); err != nil {
		t.Fatal(err)
	}
	if len(protocol.addedFlags) != 1 || protocol.addedFlags[0][0] != "\\Seen" {
		t.Fatalf("flag mutation = %+v", protocol.addedFlags)
	}
	if err := provider.Apply(context.Background(), mail.RemoteAction{Kind: string(mail.ActionArchive), TargetIDs: []string{"message-1"}}); err != nil {
		t.Fatal(err)
	}
	if len(store.moves) != 1 || store.moves[0][1].MailboxRemoteID != "archive" || store.moves[0][1].UIDValidity != 8 || store.moves[0][1].UID != 21 {
		t.Fatalf("persisted move = %+v", store.moves)
	}
}

func TestProviderDraftSendAmbiguityAndAttachmentDownload(t *testing.T) {
	raw := []byte("From: Owner <owner@example.test>\r\nTo: One <one@example.test>\r\nMessage-ID: <draft@example.test>\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nhello\r\n--x\r\nContent-Type: text/plain; name=note.txt\r\nContent-Disposition: attachment; filename=note.txt\r\n\r\nsecret\r\n--x--\r\n")
	store := &providerFixtureStore{
		folders: []FolderState{
			providerFolder("drafts", "Drafts", mail.MailboxDrafts, 2, 3),
			providerFolder("sent", "Sent", mail.MailboxSent, 4, 5),
		},
		locations: []MessageLocation{{MessageRemoteID: "message", MailboxRemoteID: "sent", WireName: "Sent", UIDValidity: 4, UID: 8}},
	}
	protocol := &providerFixtureProtocol{appendResult: AppendResult{UIDValidity: 2, UID: 9}, raw: raw}
	provider := newProviderFixture(t, store, protocol)
	draftID, err := provider.SaveDraft(context.Background(), mail.OutgoingMessage{Raw: bytes.NewReader(raw)})
	if err != nil || !strings.HasPrefix(draftID, "imap:draft:v1_") {
		t.Fatalf("save draft id=%q error=%v", draftID, err)
	}
	if err := provider.DeleteDraft(context.Background(), draftID); err != nil || len(protocol.deleted) != 1 || protocol.deleted[0].UID != 9 {
		t.Fatalf("delete draft = %+v error=%v", protocol.deleted, err)
	}
	download, err := provider.DownloadAttachment(context.Background(), "message", "part:0")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(download)
	_ = download.Close()
	if string(payload) != "secret" {
		t.Fatalf("attachment payload = %q", payload)
	}
	protocol.smtpErr = errors.New("connection lost after DATA")
	if _, err := provider.Send(context.Background(), mail.OutgoingMessage{Raw: bytes.NewReader(raw)}); !errors.Is(err, ErrDeliveryAmbiguous) || protocol.smtpCalls != 1 {
		t.Fatalf("ambiguous send calls=%d error=%v", protocol.smtpCalls, err)
	}
	protocol.smtpErr = nil
	sentID, err := provider.Send(context.Background(), mail.OutgoingMessage{Raw: bytes.NewReader(raw)})
	if err != nil || sentID != stableMessageID(raw, "draft@example.test") || protocol.smtpCalls != 2 {
		t.Fatalf("confirmed send id=%q calls=%d error=%v", sentID, protocol.smtpCalls, err)
	}
}

func TestSMTPEnvelopeKeepsBccRecipientButStripsHeader(t *testing.T) {
	raw := []byte("From: owner@example.test\r\nTo: one@example.test\r\nBcc: hidden@example.test\r\n\t, second@example.test\r\nSubject: fixture\r\n\r\nbody")
	from, recipients, err := smtpEnvelope(raw)
	if err != nil || from != "owner@example.test" || len(recipients) != 3 {
		t.Fatalf("envelope from=%q recipients=%v error=%v", from, recipients, err)
	}
	clean := withoutBccHeader(raw)
	if bytes.Contains(bytes.ToLower(clean), []byte("bcc:")) || !bytes.Contains(clean, []byte("Subject: fixture")) || !bytes.HasSuffix(clean, []byte("body")) {
		t.Fatalf("stripped payload = %q", clean)
	}
}

func newProviderFixture(t *testing.T, store ProviderStore, protocol Protocol) *Provider {
	t.Helper()
	normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if err != nil {
		t.Fatal(err)
	}
	credentials, _ := json.Marshal(storedCredentials{
		Username: "owner@example.test", Password: "app-password",
		IMAP: ServerConfig{Host: "imap.example.test", Port: 993, TLSMode: TLSImplicit},
		SMTP: ServerConfig{Host: "smtp.example.test", Port: 587, TLSMode: TLSStartTLS},
	})
	provider, err := NewProvider("0199ed3b-c950-7000-8000-000000000001", "0199ed3b-c950-7000-8000-000000000002", credentials, map[string]bool{"imap.uidplus": true, "imap.move": true}, store, protocol, normalizer)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func providerFolder(remoteID, wireName string, role mail.MailboxRole, validity, next int64) FolderState {
	return FolderState{RemoteID: remoteID, WireName: wireName, Name: wireName, Role: role, Selectable: true, UIDValidity: &validity, UIDNext: &next, NextUID: &next, CursorState: FolderCursorActive}
}

func fixtureMIME(messageID, reference, subject, body string) []byte {
	references := ""
	if reference != "" {
		references = "References: <" + reference + ">\r\nIn-Reply-To: <" + reference + ">\r\n"
	}
	return []byte("From: sender@example.test\r\nTo: owner@example.test\r\nMessage-ID: <" + messageID + ">\r\n" + references + "Subject: " + subject + "\r\n\r\n" + body)
}
