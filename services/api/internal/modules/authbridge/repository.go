package authbridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrInvalidSubject = errors.New("invalid authenticated subject")
	ErrUserNotFound   = errors.New("authenticated user not found")
)

type userQuerier interface {
	GetUserByAuthSubject(context.Context, pgtype.UUID) (dbgen.GetUserByAuthSubjectRow, error)
}

type Repository struct {
	queries userQuerier
}

func NewRepository(queries userQuerier) *Repository {
	return &Repository{queries: queries}
}

func (repository *Repository) FindBySubject(ctx context.Context, subject string) (User, error) {
	id, err := uuid.Parse(subject)
	if err != nil {
		return User{}, ErrInvalidSubject
	}
	row, err := repository.queries.GetUserByAuthSubject(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load authenticated user: %w", err)
	}
	return User{ID: uuid.UUID(row.ID.Bytes).String(), Email: row.Email, Locale: row.Locale}, nil
}
