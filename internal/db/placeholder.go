package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig contains database pool options.
type PoolConfig struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

type pool interface {
	Ping(ctx context.Context) error
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
	Close()
}

// NewPoolConfig builds database pool config from application settings.
func NewPoolConfig(dsn string, maxConns, minConns, maxConnLifetimeSec, maxConnIdleSec int) PoolConfig {
	return PoolConfig{
		DSN:             dsn,
		MaxConns:        int32(maxConns),
		MinConns:        int32(minConns),
		MaxConnLifetime: time.Duration(maxConnLifetimeSec) * time.Second,
		MaxConnIdleTime: time.Duration(maxConnIdleSec) * time.Second,
	}
}

// Client wraps a PostgreSQL connection pool and transaction helper.
type Client struct {
	mu   sync.RWMutex
	cfg  PoolConfig
	pool pool
}

// NewClient creates a database client with pool config.
func NewClient(cfg PoolConfig) *Client {
	return &Client{cfg: cfg}
}

// Connect initializes pool and verifies connectivity.
func (c *Client) Connect(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.DSN) == "" {
		return fmt.Errorf("database dsn is empty")
	}

	poolCfg, err := pgxpool.ParseConfig(c.cfg.DSN)
	if err != nil {
		return fmt.Errorf("parse database dsn: %w", err)
	}
	poolCfg.MaxConns = c.cfg.MaxConns
	poolCfg.MinConns = c.cfg.MinConns
	poolCfg.MaxConnLifetime = c.cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = c.cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("create pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("ping database: %w", err)
	}

	c.mu.Lock()
	c.pool = pool
	c.mu.Unlock()

	return nil
}

// Ping checks current database connectivity.
func (c *Client) Ping(ctx context.Context) error {
	pool, err := c.getPool()
	if err != nil {
		return err
	}
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close releases the pool resources.
func (c *Client) Close() error {
	c.mu.Lock()
	pool := c.pool
	c.pool = nil
	c.mu.Unlock()

	if pool != nil {
		pool.Close()
	}
	return nil
}

// WithTx runs business logic in a single transaction.
func (c *Client) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) (err error) {
	pool, err := c.getPool()
	if err != nil {
		return err
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(ctx)
			panic(recovered)
		}

		if err != nil {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				err = fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
			}
			return
		}

		if commitErr := tx.Commit(ctx); commitErr != nil {
			err = fmt.Errorf("commit transaction: %w", commitErr)
		}
	}()

	if execErr := fn(tx); execErr != nil {
		err = fmt.Errorf("execute transaction function: %w", execErr)
		return err
	}

	return nil
}

// Health is used by HTTP health checks.
func (c *Client) Health(ctx context.Context) error {
	return c.Ping(ctx)
}

func (c *Client) getPool() (pool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.pool == nil {
		return nil, fmt.Errorf("database is not connected")
	}
	return c.pool, nil
}
