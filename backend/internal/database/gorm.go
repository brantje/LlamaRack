package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
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
	}), gormConfig())
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &Connection{orm: orm, raw: raw, dialect: DialectSQLite}, nil
}

// OpenConfigured selects SQLite when databaseURL is empty and PostgreSQL when
// it is explicitly configured. An invalid or unreachable PostgreSQL database
// is an error; this function never silently falls back to SQLite.
func OpenConfigured(ctx context.Context, sqlitePath, databaseURL string) (*Connection, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return OpenStore(ctx, sqlitePath)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return nil, errors.New("database URL must use postgres:// or postgresql://")
	}
	orm, err := gorm.Open(postgres.Open(databaseURL), gormConfig())
	if err != nil {
		return nil, redactDatabaseError("open PostgreSQL database", databaseURL, err)
	}
	raw, err := orm.DB()
	if err != nil {
		return nil, redactDatabaseError("initialize PostgreSQL pool", databaseURL, err)
	}
	raw.SetMaxOpenConns(25)
	raw.SetMaxIdleConns(5)
	raw.SetConnMaxLifetime(30 * time.Minute)
	if err := raw.PingContext(ctx); err != nil {
		_ = raw.Close()
		return nil, redactDatabaseError("connect PostgreSQL database", databaseURL, err)
	}
	if _, err := migratePostgres(ctx, raw); err != nil {
		_ = raw.Close()
		return nil, redactDatabaseError("migrate PostgreSQL database", databaseURL, err)
	}
	return &Connection{orm: orm, raw: raw, dialect: DialectPostgres}, nil
}

func gormConfig() *gorm.Config {
	return &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Silent),
	}
}

func redactDatabaseError(prefix, dsn string, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	message = strings.ReplaceAll(message, dsn, "<redacted>")
	if parsed, parseErr := url.Parse(dsn); parseErr == nil && parsed.User != nil {
		if password, ok := parsed.User.Password(); ok && password != "" {
			message = strings.ReplaceAll(message, password, "<redacted>")
		}
	}
	return fmt.Errorf("%s: %s", prefix, message)
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

func (c *Connection) query(query string) string {
	if c != nil && c.dialect == DialectPostgres {
		return strings.ReplaceAll(query, "unixepoch()", "CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)")
	}
	return query
}

type result struct{ rows int64 }

func (r result) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is not supported; use INSERT ... RETURNING")
}
func (r result) RowsAffected() (int64, error) { return r.rows, nil }

func (c *Connection) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if c == nil || c.orm == nil {
		return nil, errors.New("database store is closed")
	}
	tx := c.orm.WithContext(ctx).Exec(c.query(query), args...)
	if tx.Error != nil {
		return nil, ClassifyError(tx.Error)
	}
	return result{rows: tx.RowsAffected}, nil
}

func (c *Connection) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if c == nil || c.orm == nil {
		return nil, errors.New("database store is closed")
	}
	rows, err := c.orm.WithContext(ctx).Raw(c.query(query), args...).Rows()
	if err != nil {
		return nil, ClassifyError(err)
	}
	return rows, nil
}

func (c *Connection) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return c.orm.WithContext(ctx).Raw(c.query(query), args...).Row()
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

func (t *gormTransaction) query(query string) string {
	if t != nil && t.dialect == DialectPostgres {
		return strings.ReplaceAll(query, "unixepoch()", "CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)")
	}
	return query
}

func (t *gormTransaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if t == nil || t.orm == nil {
		return nil, errors.New("database transaction is closed")
	}
	tx := t.orm.WithContext(ctx).Exec(t.query(query), args...)
	if tx.Error != nil {
		return nil, ClassifyError(tx.Error)
	}
	return result{rows: tx.RowsAffected}, nil
}

func (t *gormTransaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if t == nil || t.orm == nil {
		return nil, errors.New("database transaction is closed")
	}
	rows, err := t.orm.WithContext(ctx).Raw(t.query(query), args...).Rows()
	if err != nil {
		return nil, ClassifyError(err)
	}
	return rows, nil
}

func (t *gormTransaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.orm.WithContext(ctx).Raw(t.query(query), args...).Row()
}

func (t *gormTransaction) Commit() error {
	if t == nil || t.orm == nil {
		return errors.New("database transaction is closed")
	}
	return ClassifyError(t.orm.Commit().Error)
}

func (t *gormTransaction) Rollback() error {
	if t == nil || t.orm == nil {
		return nil
	}
	return t.orm.Rollback().Error
}
