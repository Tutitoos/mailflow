package sentry

import (
	"errors"
	"time"
)

var ErrEnvelopeTooLarge = errors.New("sentry envelope exceeds the configured limit")

type Receipt struct {
	ID         string    `json:"id"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type Service struct {
	maxEnvelopeBytes int
}

func NewService(maxEnvelopeBytes int) *Service {
	return &Service{maxEnvelopeBytes: maxEnvelopeBytes}
}

func (s *Service) Accept(id string, envelope []byte) (Receipt, error) {
	if len(envelope) > s.maxEnvelopeBytes {
		return Receipt{}, ErrEnvelopeTooLarge
	}
	return Receipt{ID: id, ReceivedAt: time.Now().UTC()}, nil
}
