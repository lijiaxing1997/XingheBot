package cluster

import "context"

type PresenceStore interface {
	Upsert(ctx context.Context, info SlaveInfo, ownerInstanceID string, ttlSeconds int) error
	Delete(ctx context.Context, slaveID string) error
	Close() error
}

type NoopPresenceStore struct{}

func (NoopPresenceStore) Upsert(ctx context.Context, info SlaveInfo, ownerInstanceID string, ttlSeconds int) error {
	return nil
}

func (NoopPresenceStore) Delete(ctx context.Context, slaveID string) error {
	return nil
}

func (NoopPresenceStore) Close() error {
	return nil
}

