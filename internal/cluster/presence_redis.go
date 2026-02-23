package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisPresenceStore struct {
	client *redis.Client
}

func NewRedisPresenceStore(redisURL string) (*RedisPresenceStore, error) {
	url := strings.TrimSpace(redisURL)
	if url == "" {
		return nil, errors.New("redis url is required")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisPresenceStore{client: client}, nil
}

func (s *RedisPresenceStore) Upsert(ctx context.Context, info SlaveInfo, ownerInstanceID string, ttlSeconds int) error {
	if s == nil || s.client == nil {
		return nil
	}
	id := strings.TrimSpace(info.SlaveID)
	if id == "" {
		return errors.New("slave_id is required")
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 15
	}
	ttl := time.Duration(ttlSeconds) * time.Second

	type presenceRecord struct {
		SlaveInfo
		OwnerInstance string `json:"owner_instance"`
	}
	rec := presenceRecord{
		SlaveInfo:      info,
		OwnerInstance:  strings.TrimSpace(ownerInstanceID),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	keyPresence := fmt.Sprintf("gateway:slave:%s", id)
	keyRoute := fmt.Sprintf("gateway:route:%s", id)

	pipe := s.client.Pipeline()
	pipe.Set(ctx, keyPresence, data, ttl)
	pipe.Set(ctx, keyRoute, strings.TrimSpace(ownerInstanceID), ttl)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisPresenceStore) Delete(ctx context.Context, slaveID string) error {
	if s == nil || s.client == nil {
		return nil
	}
	id := strings.TrimSpace(slaveID)
	if id == "" {
		return nil
	}
	keyPresence := fmt.Sprintf("gateway:slave:%s", id)
	keyRoute := fmt.Sprintf("gateway:route:%s", id)
	return s.client.Del(ctx, keyPresence, keyRoute).Err()
}

func (s *RedisPresenceStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

