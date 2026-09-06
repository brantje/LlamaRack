package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, privateDirPerm); err != nil {
		return nil, err
	}
	if shouldRestrictDir(dir) {
		if err := restrictMode(dir, privateDirPerm, false); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite pragma: %w", err)
		}
	}
	if _, err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := restrictSQLiteFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
