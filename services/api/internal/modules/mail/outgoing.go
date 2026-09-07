package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"mime"
	"mime/multipart"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"sync"
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

type DeliveryService struct {
	pool      *pgxpool.Pool
	drafts    DraftRepository
	providers OutgoingProviderResolver
	publisher ActionEventPublisher
	locks     sync.Map
}

func NewDeliveryService(pool *pgxpool.Pool, drafts DraftRepository, providers OutgoingProviderResolver, publisher ActionEventPublisher) (*DeliveryService, error) {
	if pool == nil || drafts == nil || providers == nil {
		return nil, errors.New("mail delivery configuration is invalid")
	}
	return &DeliveryService{pool: pool, drafts: drafts, providers: providers, publisher: publisher}, nil
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
	raw, err := buildOutgoingMIME(draft, context)
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

func buildOutgoingMIME(draft Draft, source outgoingContext) ([]byte, error) {
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
	if draft.BodyHTML == "" {
		writeHeader("Content-Type", `text/plain; charset="UTF-8"`)
		writeHeader("Content-Transfer-Encoding", "8bit")
		output.WriteString("\r\n")
		output.WriteString(strings.ReplaceAll(draft.BodyText, "\n", "\r\n"))
		return output.Bytes(), nil
	}
	writer := multipart.NewWriter(&output)
	if err := writer.SetBoundary("mailflow-alternative"); err != nil {
		return nil, ErrInvalidDraft
	}
	writeHeader("Content-Type", `multipart/alternative; boundary="mailflow-alternative"`)
	output.WriteString("\r\n")
	for _, part := range []struct{ mediaType, body string }{{"text/plain", draft.BodyText}, {"text/html", draft.BodyHTML}} {
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", part.mediaType+`; charset="UTF-8"`)
		header.Set("Content-Transfer-Encoding", "8bit")
		partWriter, err := writer.CreatePart(header)
		if err != nil {
			return nil, ErrInvalidDraft
		}
		_, _ = partWriter.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n")))
	}
	if err := writer.Close(); err != nil {
		return nil, ErrInvalidDraft
	}
	return output.Bytes(), nil
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
