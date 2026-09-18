package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Querier is the minimal database execution surface used by persistence adapters.
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Store is the generic SQL execution substrate used by SQL adapters and
// application composition. Domain services depend on package-owned store
// interfaces instead of this SQL-shaped surface.
type Store interface {
	Querier
	Close() error
}

// Transaction is an opaque transaction handle.
type Transaction interface {
	Querier
	Commit() error
	Rollback() error
}

type transactionStarter interface {
	beginTransaction(context.Context) (Transaction, error)
}

// Begin starts a transaction without exposing a concrete *sql.Tx.
func Begin(ctx context.Context, store Store) (Transaction, error) {
	if store == nil {
		return nil, errors.New("database store is required")
	}
	if starter, ok := store.(transactionStarter); ok {
		return starter.beginTransaction(ctx)
	}
	if db, ok := store.(*sql.DB); ok {
		return db.BeginTx(ctx, nil)
	}
	return nil, fmt.Errorf("database store %T does not support transactions", store)
}
