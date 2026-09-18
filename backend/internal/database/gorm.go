package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
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
		query = strings.ReplaceAll(query, "unixepoch()", "CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)")
		query = bindPostgresPlaceholders(query)
	}
	return query
}

// bindPostgresPlaceholders converts the adapter's portable question-mark
// placeholders to PostgreSQL's $n form without touching quoted SQL literals
// or identifiers. Runtime adapter SQL does not rely on GORM statement building
// for this translation.
func bindPostgresPlaceholders(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 8)
	placeholder := 1
	inSingle, inDouble := false, false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if inSingle {
			out.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					out.WriteByte(query[i+1])
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			out.WriteByte(ch)
			if ch == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					out.WriteByte(query[i+1])
					i++
				} else {
					inDouble = false
				}
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
			out.WriteByte(ch)
		case '"':
			inDouble = true
			out.WriteByte(ch)
		case '?':
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(placeholder))
			placeholder++
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

type result struct{ rows int64 }

func (r result) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is not supported; use INSERT ... RETURNING")
}
func (r result) RowsAffected() (int64, error) { return r.rows, nil }

func (c *Connection) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if c == nil || c.raw == nil {
		return nil, errors.New("database store is closed")
	}
	res, err := c.raw.ExecContext(ctx, c.query(query), args...)
	if err != nil {
		return nil, ClassifyError(err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, ClassifyError(err)
	}
	return result{rows: rows}, nil
}

func (c *Connection) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if c == nil || c.raw == nil {
		return nil, errors.New("database store is closed")
	}
	rows, err := c.raw.QueryContext(ctx, c.query(query), args...)
	if err != nil {
		return nil, ClassifyError(err)
	}
	return rows, nil
}

func (c *Connection) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if c == nil || c.raw == nil {
		return (&sql.DB{}).QueryRowContext(ctx, query, args...)
	}
	return c.raw.QueryRowContext(ctx, c.query(query), args...)
}

func (c *Connection) beginTransaction(ctx context.Context) (Transaction, error) {
	if c == nil || c.raw == nil {
		return nil, errors.New("database store is closed")
	}
	tx, err := c.raw.BeginTx(ctx, nil)
	if err != nil {
		return nil, ClassifyError(err)
	}
	return &sqlTransaction{tx: tx, dialect: c.dialect}, nil
}

type sqlTransaction struct {
	tx      *sql.Tx
	dialect Dialect
}

func (t *sqlTransaction) query(query string) string {
	if t != nil && t.dialect == DialectPostgres {
		query = strings.ReplaceAll(query, "unixepoch()", "CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)")
		query = bindPostgresPlaceholders(query)
	}
	return query
}

func (t *sqlTransaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if t == nil || t.tx == nil {
		return nil, errors.New("database transaction is closed")
	}
	res, err := t.tx.ExecContext(ctx, t.query(query), args...)
	if err != nil {
		return nil, ClassifyError(err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, ClassifyError(err)
	}
	return result{rows: rows}, nil
}

func (t *sqlTransaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if t == nil || t.tx == nil {
		return nil, errors.New("database transaction is closed")
	}
	rows, err := t.tx.QueryContext(ctx, t.query(query), args...)
	if err != nil {
		return nil, ClassifyError(err)
	}
	return rows, nil
}

func (t *sqlTransaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.query(query), args...)
}

func (t *sqlTransaction) Commit() error {
	if t == nil || t.tx == nil {
		return errors.New("database transaction is closed")
	}
	return ClassifyError(t.tx.Commit())
}

func (t *sqlTransaction) Rollback() error {
	if t == nil || t.tx == nil {
		return nil
	}
	return ClassifyError(t.tx.Rollback())
}
