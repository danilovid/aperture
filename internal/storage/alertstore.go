package storage

import (
	"context"
	"sync"
)

// AlertStore keeps each organization's alert settings. They are opaque bytes
// here — the alerter owns their shape — and a secret as a whole: a Slack or
// Telegram webhook address is itself the credential to post to it.
type AlertStore interface {
	// GetAlertSettings returns the organization's settings; ok is false
	// when it has none of its own.
	GetAlertSettings(ctx context.Context, orgID string) (raw []byte, ok bool, err error)
	SetAlertSettings(ctx context.Context, orgID string, raw []byte) error
}

// MemAlertStore keeps alert settings in memory, for tests.
type MemAlertStore struct {
	mu    sync.RWMutex
	byOrg map[string][]byte
}

var _ AlertStore = (*MemAlertStore)(nil)

func NewMemAlertStore() *MemAlertStore { return &MemAlertStore{byOrg: map[string][]byte{}} }

func (s *MemAlertStore) GetAlertSettings(_ context.Context, orgID string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, ok := s.byOrg[orgID]
	return append([]byte(nil), raw...), ok, nil
}

func (s *MemAlertStore) SetAlertSettings(_ context.Context, orgID string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byOrg[orgID] = append([]byte(nil), raw...)
	return nil
}
