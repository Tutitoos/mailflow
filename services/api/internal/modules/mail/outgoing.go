package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	stdmail "net/mail"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DeliveryStatus string

const (
	DeliveryPrepared  DeliveryStatus = "prepared"
	DeliverySending   DeliveryStatus = "sending"
	DeliverySent      DeliveryStatus = "sent"
	DeliveryAmbiguous DeliveryStatus = "ambiguous"
)

var (
	ErrInvalidDelivery  = errors.New("invalid outbound delivery")
	ErrDeliveryConflict = errors.New("outbound idempotency key conflict")
)

type Delivery struct {
	ID        string         `json:"id"`
	AccountID string         `json:"accountId"`
	DraftID   string         `json:"draftId"`
	Status    DeliveryStatus `json:"status"`
	RemoteID  *string        `json:"remoteId"`
}

type OutgoingProvider interface {
	SaveDraft(context.Context, OutgoingMessage) (string, error)
	Send(context.Context, OutgoingMessage) (string, error)
}

type OutgoingProviderResolver interface {
	ResolveOutgoingProvider(context.Context, string, string) (OutgoingProvider, error)
}

type OutgoingAttachmentStore interface {
	OpenAttachment(context.Context, string, string, time.Time) (cdn.Attachment, *os.File, error)
}

type DeliveryService struct {
	pool      *pgxpool.Pool
	drafts    DraftRepository
	providers OutgoingProviderResolver
	publisher ActionEventPublisher
	objects   OutgoingAttachmentStore
	locks     sync.Map
}

func NewDeliveryService(pool *pgxpool.Pool, drafts DraftRepository, providers OutgoingProviderResolver, publisher ActionEventPublisher, objects ...OutgoingAttachmentStore) (*DeliveryService, error) {
	if pool == nil || drafts == nil || providers == nil {
		return nil, errors.New("mail delivery configuration is invalid")
	}
	var objectStore OutgoingAttachmentStore
	if len(objects) > 0 {
		objectStore = objects[0]
	}
	return &DeliveryService{pool: pool, drafts: drafts, providers: providers, publisher: publisher, objects: objectStore}, nil
}

func (service *DeliveryService) SaveDraft(ctx context.Context, input CreateDraftInput) (Draft, error) {
	draft, err := service.drafts.CreateDraft(ctx, input)
	if err == nil {
		service.publishDraft(ctx, input.UserID, draft, "saved")
	}
	return draft, err
}

func (service *DeliveryService) GetDraft(ctx context.Context, user, account, draftID string) (Draft, error) {
	return service.drafts.GetDraft(ctx, user, account, draftID)
}

func (service *DeliveryService) UpdateDraft(ctx context.Context, input UpdateDraftInput) (Draft, error) {
	draft, err := service.drafts.UpdateDraft(ctx, input)
	if err == nil {
		service.publishDraft(ctx, input.UserID, draft, "saved")
	}
	return draft, err
}

func (service *DeliveryService) DiscardDraft(ctx context.Context, user, account, draftID string) (Draft, error) {
	draft, err := service.drafts.DiscardDraft(ctx, user, account, draftID)
	if err == nil {
		service.publishDraft(ctx, user, draft, "discarded")
	}
	return draft, err
}

func (service *DeliveryService) CheckpointDraft(ctx context.Context, user, account, draftID string) (Draft, error) {
	lockValue, _ := service.locks.LoadOrStore(draftID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	draft, err := service.drafts.GetDraft(ctx, user, account, draftID)
	if err != nil || draft.SyncStatus == DraftConflict || draft.SyncStatus == DraftDiscarded {
		if err == nil {
			err = ErrDraftConflict
		}
		return Draft{}, err
	}
	if draft.SyncedRevision == draft.LocalRevision && draft.RemoteID != nil {
		return draft, nil
	}
	raw, threadID, err := service.build(ctx, user, draft)
	if err != nil {
		return Draft{}, err
	}
	provider, err := service.providers.ResolveOutgoingProvider(ctx, user, account)
	if err != nil {
		return Draft{}, err
	}
	remoteID := ""
	if draft.RemoteID != nil {
		remoteID = *draft.RemoteID
	}
	remoteID, err = provider.SaveDraft(ctx, OutgoingMessage{DraftID: remoteID, ThreadID: threadID, Raw: bytes.NewReader(raw)})
	if err != nil {
		return Draft{}, err
	}
	checkpoint, err := service.drafts.CheckpointRemote(ctx, RemoteDraftCheckpoint{UserID: user, AccountID: account, DraftID: draft.ID, LocalRevision: draft.LocalRevision, RemoteID: remoteID, RemoteRevision: remoteID})
	if err == nil {
		service.publishDraft(ctx, user, checkpoint, "synced")
	}
	return checkpoint, err
}

func (service *DeliveryService) SendDraft(ctx context.Context, user, account, draftID string, expectedRevision int64, key string) (Delivery, error) {
	if expectedRevision < 1 || !validIdempotencyKey(key) {
		return Delivery{}, ErrInvalidDelivery
	}
	if _, _, err := ownerAccountIDs(user, account); err != nil {
		return Delivery{}, ErrInvalidDelivery
	}
	if _, err := databaseID(draftID); err != nil {
		return Delivery{}, ErrInvalidDelivery
	}
	if existing, _, existingErr := service.getDelivery(ctx, user, account, key); existingErr == nil {
		if existing.DraftID != draftID {
			return Delivery{}, ErrDeliveryConflict
		}
		return existing, nil
	} else if !errors.Is(existingErr, pgx.ErrNoRows) {
		return Delivery{}, existingErr
	}
	draft, err := service.drafts.GetDraft(ctx, user, account, draftID)
	if err != nil {
		return Delivery{}, err
	}
	if draft.LocalRevision != expectedRevision || draft.SyncStatus == DraftConflict || draft.SyncStatus == DraftDiscarded || len(draft.Recipients) == 0 {
		return Delivery{}, ErrDraftConflict
	}
	raw, threadID, err := service.build(ctx, user, draft)
	if err != nil {
		return Delivery{}, err
	}
	payloadHash := sha256.Sum256(raw)
	delivery, created, err := service.prepareDelivery(ctx, user, draft, key, payloadHash[:])
	if err != nil || (!created && delivery.Status != DeliveryPrepared) {
		return delivery, err
	}
	provider, err := service.providers.ResolveOutgoingProvider(ctx, user, account)
	if err != nil {
		return delivery, err
	}
	delivery, claimed, err := service.claimDelivery(ctx, user, delivery.ID)
	if err != nil {
		return delivery, err
	}
	if !claimed {
		return delivery, nil
	}
	remoteID, sendErr := provider.Send(ctx, OutgoingMessage{ThreadID: threadID, Raw: bytes.NewReader(raw)})
	if sendErr != nil {
		return service.finishDelivery(ctx, user, delivery.ID, DeliveryAmbiguous, "")
	}
	delivery, err = service.finishDelivery(ctx, user, delivery.ID, DeliverySent, remoteID)
	if err != nil {
		return Delivery{}, err
	}
	if _, err := service.drafts.DiscardDraft(ctx, user, account, draftID); err != nil {
		return Delivery{}, err
	}
	service.publishDraft(ctx, user, draft, "sent")
	return delivery, nil
}

func (service *DeliveryService) build(ctx context.Context, user string, draft Draft) ([]byte, string, error) {
	context := outgoingContext{}
	if draft.SourceMessageID != nil {
		userID, accountID, messageID, err := scopedResourceIDs(user, draft.AccountID, *draft.SourceMessageID)
		if err != nil {
			return nil, "", ErrInvalidDraft
		}
		err = service.pool.QueryRow(ctx, `select threads.remote_id, coalesce(messages.message_id, ''), messages.references_header
from messages join threads on threads.id = messages.thread_id and threads.account_id = messages.account_id
join accounts on accounts.id = messages.account_id
where messages.id = $1 and messages.account_id = $2 and accounts.user_id = $3`, messageID, accountID, userID).Scan(&context.threadID, &context.messageID, &context.references)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrDraftNotFound
		}
		if err != nil {
			return nil, "", err
		}
	}
	attachments, err := service.loadAttachments(ctx, user, draft)
	if err != nil {
		return nil, "", err
	}
	raw, err := buildOutgoingMIME(draft, context, attachments)
	threadID := ""
	if draft.Mode == ComposeReply {
		threadID = context.threadID
	}
	return raw, threadID, err
}

type outgoingContext struct {
	threadID   string
	messageID  string
	references []string
}

type outgoingAttachment struct {
	filename  string
	mediaType string
	content   []byte
}

const maxOutgoingAttachmentBytes = 20 << 20

func (service *DeliveryService) loadAttachments(ctx context.Context, user string, draft Draft) ([]outgoingAttachment, error) {
	if len(draft.Attachments) == 0 {
		return nil, nil
	}
	if service.objects == nil {
		return nil, ErrDraftConflict
	}
	result := make([]outgoingAttachment, 0, len(draft.Attachments))
	total := int64(0)
	for _, attachment := range draft.Attachments {
		metadata, file, err := service.objects.OpenAttachment(ctx, user, attachment.ObjectID, time.Now().UTC())
		if err != nil {
			return nil, ErrDraftConflict
		}
		if metadata.AccountID != draft.AccountID || metadata.SizeBytes != attachment.SizeBytes || metadata.MediaType != attachment.MediaType || total+metadata.SizeBytes > maxOutgoingAttachmentBytes {
			_ = file.Close()
			return nil, ErrInvalidDraft
		}
		content, readErr := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, source: file}, metadata.SizeBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || int64(len(content)) != metadata.SizeBytes {
			return nil, ErrDraftConflict
		}
		total += metadata.SizeBytes
		filename := "attachment"
		if attachment.Filename != nil {
			filename = *attachment.Filename
		}
		result = append(result, outgoingAttachment{filename: filename, mediaType: metadata.MediaType, content: content})
	}
	return result, nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(destination []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.source.Read(destination)
	}
}

func buildOutgoingMIME(draft Draft, source outgoingContext, attachments []outgoingAttachment) ([]byte, error) {
	var output bytes.Buffer
	writeHeader := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&output, "%s: %s\r\n", name, value)
		}
	}
	for _, role := range []struct {
		role AddressRole
		name string
	}{{AddressTo, "To"}, {AddressCC, "Cc"}, {AddressBCC, "Bcc"}} {
		addresses := make([]string, 0)
		for _, recipient := range draft.Recipients {
			if recipient.Role == role.role {
				name := ""
				if recipient.DisplayName != nil {
					name = *recipient.DisplayName
				}
				addresses = append(addresses, (&stdmail.Address{Name: name, Address: recipient.Address}).String())
			}
		}
		writeHeader(role.name, strings.Join(addresses, ", "))
	}
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", draft.Subject))
	if draft.Mode == ComposeReply && source.messageID != "" {
		writeHeader("In-Reply-To", "<"+strings.Trim(source.messageID, "<>")+">")
		references := append(append([]string{}, source.references...), source.messageID)
		for index := range references {
			references[index] = "<" + strings.Trim(references[index], "<>") + ">"
		}
		writeHeader("References", strings.Join(references, " "))
	}
	writeHeader("MIME-Version", "1.0")
	if len(attachments) == 0 {
		if err := writeOutgoingBody(&output, draft, true); err != nil {
			return nil, err
		}
		return output.Bytes(), nil
	}
	writeHeader("Content-Type", `multipart/mixed; boundary="mailflow-mixed"`)
	output.WriteString("\r\n")
	mixed := multipart.NewWriter(&output)
	if err := mixed.SetBoundary("mailflow-mixed"); err != nil {
		return nil, ErrInvalidDraft
	}
	bodyHeader := textproto.MIMEHeader{}
	if draft.BodyHTML == "" {
		bodyHeader.Set("Content-Type", `text/plain; charset="UTF-8"`)
		bodyHeader.Set("Content-Transfer-Encoding", "8bit")
	} else {
		bodyHeader.Set("Content-Type", `multipart/alternative; boundary="mailflow-alternative"`)
	}
	bodyWriter, err := mixed.CreatePart(bodyHeader)
	if err != nil {
		return nil, ErrInvalidDraft
	}
	if err := writeOutgoingBody(bodyWriter, draft, false); err != nil {
		return nil, err
	}
	for _, attachment := range attachments {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", mime.FormatMediaType(attachment.mediaType, map[string]string{"name": attachment.filename}))
		header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.filename}))
		header.Set("Content-Transfer-Encoding", "base64")
		part, err := mixed.CreatePart(header)
		if err != nil {
			return nil, ErrInvalidDraft
		}
		if err := writeBase64MIME(part, attachment.content); err != nil {
			return nil, ErrInvalidDraft
		}
	}
	if err := mixed.Close(); err != nil {
		return nil, ErrInvalidDraft
	}
	return output.Bytes(), nil
}

func writeBase64MIME(destination io.Writer, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	for len(encoded) > 0 {
		width := min(76, len(encoded))
		if _, err := io.WriteString(destination, encoded[:width]+"\r\n"); err != nil {
			return err
		}
		encoded = encoded[width:]
	}
	return nil
}

func writeOutgoingBody(destination io.Writer, draft Draft, includeHeaders bool) error {
	if draft.BodyHTML == "" {
		if includeHeaders {
			_, _ = io.WriteString(destination, "Content-Type: text/plain; charset=\"UTF-8\"\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
		}
		_, _ = io.WriteString(destination, strings.ReplaceAll(draft.BodyText, "\n", "\r\n"))
		return nil
	}
	if includeHeaders {
		_, _ = io.WriteString(destination, "Content-Type: multipart/alternative; boundary=\"mailflow-alternative\"\r\n\r\n")
	}
	writer := multipart.NewWriter(destination)
	if err := writer.SetBoundary("mailflow-alternative"); err != nil {
		return ErrInvalidDraft
	}
	for _, part := range []struct{ mediaType, body string }{{"text/plain", draft.BodyText}, {"text/html", draft.BodyHTML}} {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", part.mediaType+`; charset="UTF-8"`)
		header.Set("Content-Transfer-Encoding", "8bit")
		partWriter, err := writer.CreatePart(header)
		if err != nil {
			return ErrInvalidDraft
		}
		_, _ = partWriter.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n")))
	}
	if err := writer.Close(); err != nil {
		return ErrInvalidDraft
	}
	return nil
}

func (service *DeliveryService) prepareDelivery(ctx context.Context, user string, draft Draft, key string, hash []byte) (Delivery, bool, error) {
	id, err := newDatabaseID()
	if err != nil {
		return Delivery{}, false, err
	}
	result, err := service.pool.Exec(ctx, `insert into outbound_deliveries (id, account_id, draft_id, idempotency_key, payload_hash)
select $1, drafts.account_id, drafts.id, $2, $3 from drafts join accounts on accounts.id = drafts.account_id
where drafts.id = $4 and drafts.account_id = $5 and accounts.user_id = $6 and drafts.sync_status <> 'discarded'
on conflict (account_id, idempotency_key) do nothing`, id, key, hash, draft.ID, draft.AccountID, user)
	if err != nil {
		return Delivery{}, false, err
	}
	delivery, storedHash, err := service.getDelivery(ctx, user, draft.AccountID, key)
	if err != nil {
		return Delivery{}, false, err
	}
	if !bytes.Equal(storedHash, hash) || delivery.DraftID != draft.ID {
		return Delivery{}, false, ErrDeliveryConflict
	}
	return delivery, result.RowsAffected() == 1, nil
}

func (service *DeliveryService) getDelivery(ctx context.Context, user, account, key string) (Delivery, []byte, error) {
	var delivery Delivery
	var remoteID *string
	var hash []byte
	err := service.pool.QueryRow(ctx, `select outbound_deliveries.id::text, outbound_deliveries.account_id::text,
outbound_deliveries.draft_id::text, outbound_deliveries.status, outbound_deliveries.remote_id, outbound_deliveries.payload_hash
from outbound_deliveries join accounts on accounts.id = outbound_deliveries.account_id
where outbound_deliveries.account_id = $1 and outbound_deliveries.idempotency_key = $2 and accounts.user_id = $3`, account, key, user).
		Scan(&delivery.ID, &delivery.AccountID, &delivery.DraftID, &delivery.Status, &remoteID, &hash)
	delivery.RemoteID = remoteID
	return delivery, hash, err
}

func (service *DeliveryService) claimDelivery(ctx context.Context, user, id string) (Delivery, bool, error) {
	var delivery Delivery
	var remoteID *string
	err := service.pool.QueryRow(ctx, `update outbound_deliveries set status = 'sending', updated_at = now()
from accounts where outbound_deliveries.id = $1 and outbound_deliveries.status = 'prepared'
and accounts.id = outbound_deliveries.account_id and accounts.user_id = $2
returning outbound_deliveries.id::text, outbound_deliveries.account_id::text, outbound_deliveries.draft_id::text,
outbound_deliveries.status, outbound_deliveries.remote_id`, id, user).
		Scan(&delivery.ID, &delivery.AccountID, &delivery.DraftID, &delivery.Status, &remoteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, false, nil
	}
	delivery.RemoteID = remoteID
	return delivery, err == nil, err
}

func (service *DeliveryService) finishDelivery(ctx context.Context, user, id string, status DeliveryStatus, remote string) (Delivery, error) {
	if status != DeliverySent && status != DeliveryAmbiguous {
		return Delivery{}, ErrInvalidDelivery
	}
	var delivery Delivery
	var remoteID *string
	err := service.pool.QueryRow(ctx, `update outbound_deliveries set status = $1, remote_id = nullif($2, ''), updated_at = now()
from accounts where outbound_deliveries.id = $3 and outbound_deliveries.status = 'sending'
and accounts.id = outbound_deliveries.account_id and accounts.user_id = $4
returning outbound_deliveries.id::text, outbound_deliveries.account_id::text, outbound_deliveries.draft_id::text,
outbound_deliveries.status, outbound_deliveries.remote_id`, status, remote, id, user).
		Scan(&delivery.ID, &delivery.AccountID, &delivery.DraftID, &delivery.Status, &remoteID)
	delivery.RemoteID = remoteID
	return delivery, err
}

func (service *DeliveryService) publishDraft(ctx context.Context, user string, draft Draft, state string) {
	if service.publisher == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{"draftId": draft.ID, "accountId": draft.AccountID, "state": state})
	if err == nil {
		_, _ = service.publisher.Publish(ctx, user, "draft.changed", payload)
	}
}
