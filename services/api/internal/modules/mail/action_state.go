package mail

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ActionStateApplier interface {
	ApplyActionState(context.Context, pgx.Tx, EnqueueActionInput, json.RawMessage) error
}

type ActionStateStore interface {
	ActionStateApplier
	ThreadHasLabel(context.Context, string, string, string, string) (bool, error)
}

func (repository *ThreadRepositoryStore) ThreadHasLabel(ctx context.Context, user, account, thread, label string) (bool, error) {
	userID, accountID, threadID, err := scopedResourceIDs(user, account, thread)
	if err != nil {
		return false, ErrInvalidAction
	}
	labelID, err := databaseID(label)
	if err != nil {
		return false, ErrInvalidAction
	}
	var labelled bool
	err = repository.pool.QueryRow(ctx, `select exists(
  select 1
  from threads
  join accounts on accounts.id = threads.account_id
  join messages on messages.thread_id = threads.id and messages.account_id = threads.account_id
  join message_labels on message_labels.message_id = messages.id and message_labels.account_id = messages.account_id
  join labels on labels.id = message_labels.label_id and labels.account_id = message_labels.account_id
  where threads.id = $1 and threads.account_id = $2 and accounts.user_id = $3 and labels.id = $4
)`, threadID, accountID, userID, labelID).Scan(&labelled)
	if err != nil {
		return false, errors.Join(ErrInvalidAction, err)
	}
	return labelled, nil
}

func (repository *ThreadRepositoryStore) ApplyActionState(ctx context.Context, tx pgx.Tx, input EnqueueActionInput, state json.RawMessage) error {
	if tx == nil || input.TargetKind != ActionTargetThread || !validActionState(state) {
		return ErrInvalidAction
	}
	userID, accountID, targetID, err := actionIDs(input.UserID, input.AccountID, input.TargetID)
	if err != nil {
		return ErrInvalidAction
	}
	var values map[string]any
	if json.Unmarshal(state, &values) != nil {
		return ErrInvalidAction
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `select exists(
  select 1 from threads join accounts on accounts.id = threads.account_id
  where threads.id = $1 and threads.account_id = $2 and accounts.user_id = $3 and accounts.disabled_at is null
)`, targetID, accountID, userID).Scan(&authorized); err != nil || !authorized {
		return ErrThreadNotFound
	}

	switch input.Kind {
	case ActionMarkRead, ActionMarkUnread:
		value := values["read"].(bool)
		_, err = tx.Exec(ctx, `update messages set is_read = $1, updated_at = now() where thread_id = $2 and account_id = $3`, value, targetID, accountID)
		if err == nil {
			_, err = tx.Exec(ctx, `update threads set is_read = $1, updated_at = now() where id = $2 and account_id = $3`, value, targetID, accountID)
		}
	case ActionStar, ActionUnstar:
		value := values["starred"].(bool)
		_, err = tx.Exec(ctx, `update messages set is_starred = $1, updated_at = now() where thread_id = $2 and account_id = $3`, value, targetID, accountID)
		if err == nil {
			_, err = tx.Exec(ctx, `update threads set is_starred = $1, updated_at = now() where id = $2 and account_id = $3`, value, targetID, accountID)
		}
	case ActionMarkImportant, ActionMarkUnimportant:
		value := values["important"].(bool)
		_, err = tx.Exec(ctx, `update messages set is_important = $1, updated_at = now() where thread_id = $2 and account_id = $3`, value, targetID, accountID)
		if err == nil {
			_, err = tx.Exec(ctx, `update threads set is_important = $1, updated_at = now() where id = $2 and account_id = $3`, value, targetID, accountID)
		}
	case ActionMoveToTrash, ActionRestoreTrash:
		trashed := values["trashed"].(bool)
		_, err = tx.Exec(ctx, `update messages set deleted_at = case when $1 then coalesce(deleted_at, now()) else null end, updated_at = now() where thread_id = $2 and account_id = $3`, trashed, targetID, accountID)
		if err == nil {
			_, err = tx.Exec(ctx, `update threads set deleted_at = case when $1 then coalesce(deleted_at, now()) else null end, archived_at = case when $1 then archived_at else null end, updated_at = now() where id = $2 and account_id = $3`, trashed, targetID, accountID)
		}
	case ActionArchive:
		archived := values["archived"].(bool)
		_, err = tx.Exec(ctx, `update threads set archived_at = case when $1 then coalesce(archived_at, now()) else null end, updated_at = now() where id = $2 and account_id = $3`, archived, targetID, accountID)
	case ActionAddLabel, ActionRemoveLabel:
		labelID, parseErr := uuid.Parse(values["labelId"].(string))
		if parseErr != nil {
			return ErrInvalidAction
		}
		var labelAllowed bool
		if err = tx.QueryRow(ctx, `select exists(select 1 from labels join accounts on accounts.id = labels.account_id where labels.id = $1 and labels.account_id = $2 and accounts.user_id = $3)`, labelID, accountID, userID).Scan(&labelAllowed); err != nil || !labelAllowed {
			return ErrInvalidAction
		}
		if values["labelled"].(bool) {
			_, err = tx.Exec(ctx, `insert into message_labels (message_id, label_id, account_id) select id, $1, account_id from messages where thread_id = $2 and account_id = $3 on conflict do nothing`, labelID, targetID, accountID)
		} else {
			_, err = tx.Exec(ctx, `delete from message_labels where label_id = $1 and account_id = $2 and message_id in (select id from messages where thread_id = $3 and account_id = $2)`, labelID, accountID, targetID)
		}
	default:
		return ErrInvalidAction
	}
	if err != nil {
		return errors.Join(ErrOptimisticUpdate, err)
	}
	return nil
}
