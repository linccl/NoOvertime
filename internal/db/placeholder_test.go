package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakePool struct {
	pingErr    error
	beginTxErr error
	tx         *fakeTx
	closeCalls int
}

func (f *fakePool) Ping(context.Context) error {
	return f.pingErr
}

func (f *fakePool) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if f.beginTxErr != nil {
		return nil, f.beginTxErr
	}
	return f.tx, nil
}

func (f *fakePool) Close() {
	f.closeCalls++
}

type fakeTx struct {
	commitErr     error
	rollbackErr   error
	commitCalls   int
	rollbackCalls int
}

func (f *fakeTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTx) Commit(context.Context) error {
	f.commitCalls++
	return f.commitErr
}

func (f *fakeTx) Rollback(context.Context) error {
	f.rollbackCalls++
	return f.rollbackErr
}

func (f *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (f *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (f *fakeTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (f *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (f *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (f *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return nil
}

func (f *fakeTx) Conn() *pgx.Conn {
	return nil
}

func TestNewPoolConfig(t *testing.T) {
	cfg := NewPoolConfig("dsn", 20, 2, 3600, 120)

	if cfg.DSN != "dsn" {
		t.Fatalf("DSN = %q", cfg.DSN)
	}
	if cfg.MaxConns != 20 || cfg.MinConns != 2 {
		t.Fatalf("Max/MinConns = %d/%d", cfg.MaxConns, cfg.MinConns)
	}
	if cfg.MaxConnLifetime != 3600*time.Second {
		t.Fatalf("MaxConnLifetime = %s", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != 120*time.Second {
		t.Fatalf("MaxConnIdleTime = %s", cfg.MaxConnIdleTime)
	}
}

func TestPingAndHealth(t *testing.T) {
	client := NewClient(PoolConfig{})
	p := &fakePool{}
	client.pool = p

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	p.pingErr = errors.New("db down")
	err := client.Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ping database") {
		t.Fatalf("Health() error = %v", err)
	}
}

func TestWithTxCommitOnSuccess(t *testing.T) {
	tx := &fakeTx{}
	client := NewClient(PoolConfig{})
	client.pool = &fakePool{tx: tx}

	err := client.WithTx(context.Background(), func(pgx.Tx) error {
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx() error = %v", err)
	}
	if tx.commitCalls != 1 {
		t.Fatalf("commit calls = %d", tx.commitCalls)
	}
	if tx.rollbackCalls != 0 {
		t.Fatalf("rollback calls = %d", tx.rollbackCalls)
	}
}

func TestWithTxRollbackOnBusinessError(t *testing.T) {
	tx := &fakeTx{}
	client := NewClient(PoolConfig{})
	client.pool = &fakePool{tx: tx}

	err := client.WithTx(context.Background(), func(pgx.Tx) error {
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "execute transaction function") {
		t.Fatalf("error = %v", err)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d", tx.rollbackCalls)
	}
	if tx.commitCalls != 0 {
		t.Fatalf("commit calls = %d", tx.commitCalls)
	}
}

func TestWithTxRollbackFailureIncluded(t *testing.T) {
	tx := &fakeTx{rollbackErr: errors.New("rollback failed")}
	client := NewClient(PoolConfig{})
	client.pool = &fakePool{tx: tx}

	err := client.WithTx(context.Background(), func(pgx.Tx) error {
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestWithTxNotConnected(t *testing.T) {
	client := NewClient(PoolConfig{})

	err := client.WithTx(context.Background(), func(pgx.Tx) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "database is not connected") {
		t.Fatalf("WithTx() error = %v", err)
	}
}

func TestCloseClearsPool(t *testing.T) {
	client := NewClient(PoolConfig{})
	p := &fakePool{}
	client.pool = p

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if p.closeCalls != 1 {
		t.Fatalf("close calls = %d", p.closeCalls)
	}
	if _, err := client.getPool(); err == nil {
		t.Fatal("expected getPool error after Close")
	}
}
