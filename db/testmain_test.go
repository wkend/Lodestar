package db

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMain acquires the cross-package advisory lock (7337741002) FIRST — the
// lock serializes concurrent `go test ./...` processes on the PG cluster, and
// it must be held before PrepareTestDatabase drops/recreates the package's
// throwaway database, or a second concurrent run of the same package would
// DROP the database out from under the first. On top of the lock it then
// provisions the per-package database (PrepareTestDatabase — agent/db/server
// suites used to share one database and leaked rows into each other).
func TestMain(m *testing.M) {
	dsn, _, err := DSN()
	if err != nil {
		os.Exit(m.Run())
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil || conn.Ping() != nil {
		os.Exit(m.Run())
	}
	defer conn.Close()
	if _, err := conn.Exec(`SELECT pg_advisory_lock(7337741002)`); err != nil {
		os.Exit(m.Run())
	}
	defer conn.Exec(`SELECT pg_advisory_unlock(7337741002)`) //nolint:errcheck
	cleanup, ok := PrepareTestDatabase("db")
	if !ok {
		// 无库配置/不可达/非 postgres URL → m.Run() 照跑，PG 相关测试各自 skip。
		os.Exit(m.Run())
	}
	code := m.Run()
	cleanup() // os.Exit skips defers — drop the throwaway DB explicitly
	os.Exit(code)
}
