package database

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound  = errors.New("storage: not found")
	ErrConflict  = errors.New("storage: conflict")
	ErrIntegrity = errors.New("storage: integrity constraint violation")
)

const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

// ClassifyError preserves the driver error while adding portable storage
// semantics that callers can match with errors.Is.
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrIntegrity) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return fmt.Errorf("%w: %w: %w", ErrConflict, ErrIntegrity, err)
		}
		if len(pgErr.Code) >= 2 && pgErr.Code[:2] == "23" {
			return fmt.Errorf("%w: %w", ErrIntegrity, err)
		}
	}

	var sqliteErr interface{ Code() int }
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code()
		if code&0xff == 19 {
			if code == sqliteConstraintPrimaryKey || code == sqliteConstraintUnique {
				return fmt.Errorf("%w: %w: %w", ErrConflict, ErrIntegrity, err)
			}
			return fmt.Errorf("%w: %w", ErrIntegrity, err)
		}
	}
	return err
}
