package server

import (
	"database/sql"
	"os"
	"testing"

	"github.com/wkend/lodestar/db"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMain acquires the cross-package advisory lock (7337741002) FIRST — it
// serializes concurrent `go test ./...` processes and must be held before
// db.PrepareTestDatabase drops/recreates the package's throwaway database (a
// concurrent run of the same package would otherwise DROP it out from under
// the first). On top of the lock it provisions the per-package database
// (agent/db/server suites used to share one database and leaked rows into
// each other).
func TestMain(m *testing.M) {
	dsn, _, err := db.DSN()
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
	cleanup, ok := db.PrepareTestDatabase("server")
	if !ok {
		os.Exit(m.Run()) // 回退旧行为：PG 相关测试各自 skip
	}
	code := m.Run()
	cleanup() // os.Exit skips defers — drop the throwaway DB explicitly
	os.Exit(code)
}
