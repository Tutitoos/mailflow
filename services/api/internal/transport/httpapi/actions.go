package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
)

type createActionsRequest struct {
	AccountID string          `json:"accountId"`
	Kind      mail.ActionKind `json:"kind"`
	TargetIDs []string        `json:"targetIds"`
	LabelID   string          `json:"labelId,omitempty"`
}

type actionResult struct {
	TargetID string            `json:"targetId"`
	ActionID string            `json:"actionId,omitempty"`
	Status   mail.ActionStatus `json:"status,omitempty"`
	Created  bool              `json:"created,omitempty"`
	Error    string            `json:"error,omitempty"`
}

func createMailActions(service *mail.PendingActionService, state mail.ActionStateStore, threads ThreadReader) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		if service == nil || state == nil || threads == nil {
			return newProblem(fiber.StatusServiceUnavailable, "actions_unavailable", "Actions unavailable", "Mail actions are temporarily unavailable.")
		}
		key := strings.TrimSpace(c.Get("Idempotency-Key"))
		var request createActionsRequest
		if len(key) < 16 || len(key) > 96 || c.Bind().Body(&request) != nil || request.AccountID == "" || len(request.TargetIDs) < 1 || len(request.TargetIDs) > 100 {
			return invalidActionsRequest()
		}
		results := make([]actionResult, 0, len(request.TargetIDs))
		accepted := 0
		seen := make(map[string]struct{}, len(request.TargetIDs))
		for _, targetID := range request.TargetIDs {
			if _, duplicate := seen[targetID]; duplicate {
				continue
			}
			seen[targetID] = struct{}{}
			thread, err := threads.GetThread(c.Context(), user.ID, request.AccountID, targetID)
			if err != nil {
				results = append(results, actionResult{TargetID: targetID, Error: "target_unavailable"})
				continue
			}
			labelled := false
			if request.Kind == mail.ActionAddLabel || request.Kind == mail.ActionRemoveLabel {
				labelled, err = state.ThreadHasLabel(c.Context(), user.ID, request.AccountID, targetID, request.LabelID)
				if err != nil {
					results = append(results, actionResult{TargetID: targetID, Error: "target_unavailable"})
					continue
				}
			}
			desired, authoritative, valid := actionStates(request.Kind, request.LabelID, thread, labelled)
			if !valid {
				return invalidActionsRequest()
			}
			hash := sha256.Sum256([]byte(targetID))
			input := mail.EnqueueActionInput{
				UserID: user.ID, AccountID: request.AccountID, IdempotencyKey: key + ":" + hex.EncodeToString(hash[:8]),
				Kind: request.Kind, TargetKind: mail.ActionTargetThread, TargetID: targetID,
				DesiredState: desired, AuthoritativeState: authoritative, MaxAttempts: 5,
			}
			action, created, err := service.Enqueue(c.Context(), input, func(ctx context.Context, tx pgx.Tx) error {
				return state.ApplyActionState(ctx, tx, input, desired)
			})
			if errors.Is(err, mail.ErrIdempotencyConflict) {
				results = append(results, actionResult{TargetID: targetID, Error: "idempotency_conflict"})
				continue
			}
			if err != nil {
				results = append(results, actionResult{TargetID: targetID, Error: "action_rejected"})
				continue
			}
			accepted++
			results = append(results, actionResult{TargetID: targetID, ActionID: action.ID, Status: action.Status, Created: created})
		}
		if accepted == 0 {
			return newProblem(fiber.StatusConflict, "actions_rejected", "Actions rejected", "No requested mail action could be accepted.")
		}
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"items": results, "partial": accepted != len(results)})
	}
}

func actionStates(kind mail.ActionKind, labelID string, thread mail.Thread, labelled bool) (json.RawMessage, json.RawMessage, bool) {
	states := map[mail.ActionKind][2]string{
		mail.ActionMarkRead: {`{"read":true}`, `{"read":false}`}, mail.ActionMarkUnread: {`{"read":false}`, `{"read":true}`},
		mail.ActionStar: {`{"starred":true}`, `{"starred":false}`}, mail.ActionUnstar: {`{"starred":false}`, `{"starred":true}`},
		mail.ActionMarkImportant: {`{"important":true}`, `{"important":false}`}, mail.ActionMarkUnimportant: {`{"important":false}`, `{"important":true}`},
		mail.ActionMoveToTrash: {`{"trashed":true}`, `{"trashed":false}`}, mail.ActionRestoreTrash: {`{"trashed":false}`, `{"trashed":true}`},
		mail.ActionArchive: {`{"archived":true}`, `{"archived":false}`},
	}
	if kind == mail.ActionAddLabel || kind == mail.ActionRemoveLabel {
		if labelID == "" {
			return nil, nil, false
		}
		desired, _ := json.Marshal(map[string]any{"labelId": labelID, "labelled": kind == mail.ActionAddLabel})
		authoritative, _ := json.Marshal(map[string]any{"labelId": labelID, "labelled": labelled})
		return desired, authoritative, true
	}
	values, ok := states[kind]
	if !ok {
		return nil, nil, false
	}
	// Preserve the actual local truth for reversible boolean actions.
	if kind == mail.ActionMarkRead || kind == mail.ActionMarkUnread {
		values[1] = `{"read":` + strconv.FormatBool(thread.IsRead) + `}`
	} else if kind == mail.ActionStar || kind == mail.ActionUnstar {
		values[1] = `{"starred":` + strconv.FormatBool(thread.IsStarred) + `}`
	} else if kind == mail.ActionMarkImportant || kind == mail.ActionMarkUnimportant {
		values[1] = `{"important":` + strconv.FormatBool(thread.IsImportant) + `}`
	} else if kind == mail.ActionMoveToTrash || kind == mail.ActionRestoreTrash {
		values[1] = `{"trashed":` + strconv.FormatBool(thread.DeletedAt != nil) + `}`
	}
	return json.RawMessage(values[0]), json.RawMessage(values[1]), true
}

func invalidActionsRequest() error {
	return newProblem(fiber.StatusBadRequest, "invalid_mail_action", "Invalid mail action", "The action, targets, or idempotency key is invalid.")
}
