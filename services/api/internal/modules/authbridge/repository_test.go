package authbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeUserQuerier struct {
	row dbgen.GetUserByAuthSubjectRow
	err error
	id  pgtype.UUID
}

func (fake *fakeUserQuerier) GetUserByAuthSubject(_ context.Context, id pgtype.UUID) (dbgen.GetUserByAuthSubjectRow, error) {
	fake.id = id
	return fake.row, fake.err
}

func TestFindBySubject(t *testing.T) {
	id := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb776")
	query := &fakeUserQuerier{row: dbgen.GetUserByAuthSubjectRow{
		ID: pgtype.UUID{Bytes: id, Valid: true}, Email: "owner@example.test", Locale: "en",
	}}
	repository := NewRepository(query)

	user, err := repository.FindBySubject(context.Background(), id.String())
	if err != nil {
		t.Fatalf("FindBySubject() error = %v", err)
	}
	if user.ID != id.String() || user.Email != "owner@example.test" || user.Locale != "en" {
		t.Fatalf("FindBySubject() user = %#v", user)
	}
	if !query.id.Valid || query.id.Bytes != id {
		t.Fatalf("queried id = %#v", query.id)
	}
}

func TestFindBySubjectRejectsInvalidUUID(t *testing.T) {
	repository := NewRepository(&fakeUserQuerier{})
	_, err := repository.FindBySubject(context.Background(), "not-a-uuid")
	if !errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("FindBySubject() error = %v, want %v", err, ErrInvalidSubject)
	}
}

func TestFindBySubjectMapsMissingUser(t *testing.T) {
	repository := NewRepository(&fakeUserQuerier{err: pgx.ErrNoRows})
	_, err := repository.FindBySubject(context.Background(), uuid.NewString())
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("FindBySubject() error = %v, want %v", err, ErrUserNotFound)
	}
}
