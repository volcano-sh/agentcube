package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotFound indicates that the record is not found in redis.
var (
	ErrNotFound = errors.New("redis: not found")
)

// Client is the public interface so that implementations can be mocked or swapped.
type Client interface {
	GetSandboxBySessionID(ctx context.Context, sessionID string) (*Sandbox, error)
	SetSessionLockIfAbsent(ctx context.Context, sessionID string, ttl time.Duration) (bool, error)
	BindSessionWithSandbox(ctx context.Context, sessionID string, sb *Sandbox, ttl time.Duration) error
	DeleteSessionBySandboxIDTx(ctx context.Context, sandboxID string) error
}

// client is a go-redis based implementation; it is internal and only exposed via Client.
type client struct {
	rdb           *redis.Client
	sessionPrefix string
	sandboxPrefix string
	lockPrefix    string
}

// Option configures client behavior (e.g. key prefixes).
type Option func(*client)

// WithKeyPrefixes sets custom key prefixes for session / sandbox / lock.
func WithKeyPrefixes(session, sandbox, lock string) Option {
	return func(c *client) {
		c.sessionPrefix = session
		c.sandboxPrefix = sandbox
		c.lockPrefix = lock
	}
}

// NewClient creates a new Client using the given go-redis client.
func NewClient(rdb *redis.Client, opts ...Option) Client {
	c := &client{
		rdb:           rdb,
		sessionPrefix: "session:",
		sandboxPrefix: "sandbox:",
		lockPrefix:    "session_lock:",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *client) sessionKey(sessionID string) string {
	return c.sessionPrefix + sessionID
}

func (c *client) sandboxKey(sandboxID string) string {
	return c.sandboxPrefix + sandboxID
}

func (c *client) lockKey(sessionID string) string {
	return c.lockPrefix + sessionID
}

// --------- Client implementation ---------

// GetSandboxBySessionID looks up the sandbox bound to the given session ID.
// Underlying redis command: GET session:{sessionID} -> Sandbox(JSON).
func (c *client) GetSandboxBySessionID(ctx context.Context, sessionID string) (*Sandbox, error) {
	key := c.sessionKey(sessionID)

	b, err := c.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: redis GET %s: %w", key, err)
	}

	var sb Sandbox
	if err := json.Unmarshal(b, &sb); err != nil {
		return nil, fmt.Errorf("GetSandboxBySessionID: unmarshal sandbox: %w", err)
	}
	return &sb, nil
}

// SetSessionLockIfAbsent tries to acquire a lock for the given session ID.
// Underlying redis command: SETNX session_lock:{sessionID} 1 EX ttl.
func (c *client) SetSessionLockIfAbsent(ctx context.Context, sessionID string, ttl time.Duration) (bool, error) {
	key := c.lockKey(sessionID)
	ok, err := c.rdb.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("SetSessionLockIfAbsent: redis SETNX %s: %w", key, err)
	}
	return ok, nil
}

// BindSessionWithSandbox writes a bidirectional mapping between session and sandbox,
// and deletes the lock. Implemented with TxPipeline:
//
//	SETEX session:{sessionID} sandboxJSON
//	SETEX sandbox:{sandboxID} sessionID
//	DEL   session_lock:{sessionID}
func (c *client) BindSessionWithSandbox(ctx context.Context, sessionID string, sb *Sandbox, ttl time.Duration) error {
	if sb == nil {
		return errors.New("BindSessionWithSandbox: sandbox is nil")
	}
	if sb.SandboxID == "" {
		return errors.New("BindSessionWithSandbox: sandbox.SandboxID is empty")
	}

	sessionKey := c.sessionKey(sessionID)
	sandboxKey := c.sandboxKey(sb.SandboxID)
	lockKey := c.lockKey(sessionID)

	b, err := json.Marshal(sb)
	if err != nil {
		return fmt.Errorf("BindSessionWithSandbox: marshal sandbox: %w", err)
	}

	pipe := c.rdb.TxPipeline()
	pipe.Set(ctx, sessionKey, b, ttl)
	pipe.Set(ctx, sandboxKey, sessionID, ttl)
	pipe.Del(ctx, lockKey)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("BindSessionWithSandbox: redis TxPipeline EXEC: %w", err)
	}
	return nil
}

// DeleteSessionBySandboxIDTx deletes the bidirectional mapping by sandboxID.
// It uses WATCH + TxPipelined to ensure atomicity:
//
//	WATCH sandbox:{sandboxID}
//	  GET sandbox:{sandboxID} -> sessionID
//	  MULTI
//	    DEL sandbox:{sandboxID}
//	    DEL session:{sessionID}
//	  EXEC
func (c *client) DeleteSessionBySandboxIDTx(ctx context.Context, sandboxID string) error {
	sandboxKey := c.sandboxKey(sandboxID)

	for {
		err := c.rdb.Watch(ctx, func(tx *redis.Tx) error {
			sessionID, err := tx.Get(ctx, sandboxKey).Result()
			if err == redis.Nil {
				// Already deleted; treat as success.
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("redis GET %s: %w", sandboxKey, err)
			}

			sessionKey := c.sessionKey(sessionID)

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Del(ctx, sandboxKey)
				pipe.Del(ctx, sessionKey)
				return nil
			})
			if err != nil {
				return fmt.Errorf("redis TxPipelined: %w", err)
			}
			return nil
		}, sandboxKey)

		if err == redis.TxFailedErr {
			// Concurrent conflict; retry.
			continue
		}
		if err == ErrNotFound {
			// Treat not found as success.
			return nil
		}
		if err != nil {
			return fmt.Errorf("DeleteSessionBySandboxIDTx: %w", err)
		}
		return nil
	}
}
