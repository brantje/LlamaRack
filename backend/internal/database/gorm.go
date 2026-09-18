package database

import (
	"context"
	"database/sql"
	"errors"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Dialect string

const (
	DialectSQLite Dialect = "sqlite"
)

// Connection is the production persistence handle. GORM is intentionally
// encapsulated here so domain/services never receive *gorm.DB.
type Connection struct {
	orm     *gorm.DB
	raw     *sql.DB
	dialect Dialect
}

var _ Store = (*Connection)(nil)

func OpenStore(ctx context.Context, path string) (*Connection, error) {
	raw, err := Open(ctx, path)
	if err != nil {
		return nil, err
	}
	orm, err := gorm.Open(sqlite.New(sqlite.Config{
		DriverName: "sqlite",
		DSN:        path,
		Conn:       raw,
	}), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &Connection{orm: orm, raw: raw, dialect: DialectSQLite}, nil
}

func (c *Connection) Dialect() Dialect {
	if c == nil {
		return ""
	}
	return c.dialect
}

func (c *Connection) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}
	return c.raw.Close()
}

func (c *Connection) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if c == nil || c.orm == nil || c.orm.Statement == nil || c.orm.Statement.ConnPool == nil {
		return nil, errors.New("database store is closed")
	}
	// SQLite's database/sql Result carries LastInsertId, which a few existing
	// persistence paths still require. Keep that behavior while queries flow
	// through the GORM-owned connection pool.
	return c.orm.Statement.ConnPool.ExecContext(ctx, query, args...)
}

func (c *Connection) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if c == nil || c.orm == nil {
		return nil, errors.New("database store is closed")
	}
	return c.orm.WithContext(ctx).Raw(query, args...).Rows()
}

func (c *Connection) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return c.orm.WithContext(ctx).Raw(query, args...).Row()
}

func (c *Connection) beginTransaction(ctx context.Context) (Transaction, error) {
	tx := c.orm.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	return &gormTransaction{orm: tx, dialect: c.dialect}, nil
}

type gormTransaction struct {
	orm     *gorm.DB
	dialect Dialect
}

func (t *gormTransaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if t == nil || t.orm == nil || t.orm.Statement == nil || t.orm.Statement.ConnPool == nil {
		return nil, errors.New("database transaction is closed")
	}
	return t.orm.Statement.ConnPool.ExecContext(ctx, query, args...)
}

func (t *gormTransaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if t == nil || t.orm == nil {
		return nil, errors.New("database transaction is closed")
	}
	return t.orm.WithContext(ctx).Raw(query, args...).Rows()
}

func (t *gormTransaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.orm.WithContext(ctx).Raw(query, args...).Row()
}

func (t *gormTransaction) Commit() error {
	if t == nil || t.orm == nil {
		return errors.New("database transaction is closed")
	}
	return t.orm.Commit().Error
}

func (t *gormTransaction) Rollback() error {
	if t == nil || t.orm == nil {
		return nil
	}
	return t.orm.Rollback().Error
}
