package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PrepareTestDatabase provisions a throwaway database dedicated to ONE test
// package's suite and rewrites LODESTAR_PG_DSN so every DSN()/Open call inside
// the package lands in it (env has top precedence over config files).
//
// It exists because agent/db/server used to share one database: tests leaked
// rows into each other and `go test ./agent/ ./db/ ./server/` produced
// package-order-dependent failure sets that a clean-schema reset would make
// disappear again. Per-package databases make the suites hermetic against
// each other; each TestMain keeps the shared advisory-lock serialization on
// top (keeps output readable and the dev DB load bounded).
//
// Returns ok=false when no database is configured or the DSN is not a
// postgres:// URL — callers just run m.Run() and the PG-dependent tests skip
// themselves (single-database fallback is unchanged).
//
// cleanup drops the throwaway database and restores the original env. Call it
// after m.Run() returns — os.Exit skips defers.
func PrepareTestDatabase(pkgKey string) (cleanup func(), ok bool) {
	dsn, _, err := DSN()
	if err != nil {
		return nil, false
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, false // not a postgres:// URL — keep the old shared-db behavior
	}
	name := fmt.Sprintf("lodestar_test_%s", pkgKey)
	adminURL := *u
	adminURL.Path = "/postgres"
	admin, err := sql.Open("pgx", adminURL.String())
	if err != nil || admin.Ping() != nil {
		return nil, false
	}
	defer admin.Close()
	// Stale connections would block DROP; WITH (FORCE) terminates them (PG13+).
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`); err != nil {
		return nil, false
	}
	if _, err := admin.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		return nil, false
	}
	testURL := *u
	testURL.Path = "/" + name
	prev := os.Getenv("LODESTAR_PG_DSN")
	os.Setenv("LODESTAR_PG_DSN", testURL.String())
	return func() {
		if prev == "" {
			os.Unsetenv("LODESTAR_PG_DSN")
		} else {
			os.Setenv("LODESTAR_PG_DSN", prev)
		}
		// Drop best-effort; a leftover is wiped by the next run's DROP anyway.
		cleanupConn, err := sql.Open("pgx", adminURL.String())
		if err != nil {
			return
		}
		defer cleanupConn.Close()
		_, _ = cleanupConn.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`)
	}, true
}
