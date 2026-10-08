//go:build tier1

package tier1

// THE REAL SERVER RUNS AS THE ROLES PRODUCTION USES.
//
// Findings 16 and 25 were the same failure twice: the suites ran as the owner,
// the app runs as a constrained role, and a correct rule the app could not
// execute passed everything. The migration suites now end with the probe as
// argon_app (mutate.sh). This is the same step one layer up: the API suites'
// GREEN half runs the real handlers on an argon_app connection and the real
// auth service on an argon_auth connection, against the real grants.sql, as
// cmd/api does. Fixtures are still written as the owner (they are the world
// the server finds, not what it does).
//
// There is no quiet fallback: a real-server test with no role connections
// fails as a broken fixture, naming what to set. The fakes (M0, mutants,
// breaks) keep the owner's connection: grants.sql fits only the real schema.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var appDB, authDB *pgxpool.Pool

// openRolePools: with ARGON_TEST_APP_URL and ARGON_TEST_AUTH_URL set, and the
// database carrying the real grants (ARGON_TEST_GRANTS applied here, or a
// restored copy), opens the two connections. Otherwise leaves them nil.
func openRolePools(ctx context.Context, ac *pgx.Conn, name string, restored bool) int {
	appURL, authURL, grants := os.Getenv("ARGON_TEST_APP_URL"), os.Getenv("ARGON_TEST_AUTH_URL"), os.Getenv("ARGON_TEST_GRANTS")
	if appURL == "" || authURL == "" || (grants == "" && !restored) {
		return 0
	}
	if !restored {
		if _, err := ac.Exec(ctx, `GRANT CONNECT ON DATABASE `+name+` TO argon_app, argon_auth`); err != nil {
			fmt.Fprintln(os.Stderr, "tier1: grant connect:", err)
			return 2
		}
		sql, err := os.ReadFile(grants)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tier1:", err)
			return 2
		}
		// migrate's bookkeeping table, as golang-migrate creates it: the schema
		// here is applied file by file, and grants.sql revokes the app's
		// access to this table, as in production.
		sql = append([]byte(`CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
`), sql...)
		c, err := db.Acquire(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tier1:", err)
			return 2
		}
		_, err = c.Conn().PgConn().Exec(ctx, string(sql)).ReadAll()
		c.Release()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tier1: applying %s: %v\n", grants, err)
			return 2
		}
	}
	for _, p := range []struct {
		url  string
		role string
		into **pgxpool.Pool
	}{{appURL, "argon_app", &appDB}, {authURL, "argon_auth", &authDB}} {
		cfg, err := pgxpool.ParseConfig(p.url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tier1: %s url: %v\n", p.role, err)
			return 2
		}
		cfg.ConnConfig.Database = name
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tier1: %s pool: %v\n", p.role, err)
			return 2
		}
		// Read the role back from the server, not from the URL.
		var who string
		if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != p.role {
			fmt.Fprintf(os.Stderr, "tier1: the %s connection is %q (%v), not %s\n", p.role, who, err, p.role)
			return 2
		}
		*p.into = pool
	}
	return 0
}

func closeRolePools() {
	for _, p := range []*pgxpool.Pool{appDB, authDB} {
		if p != nil {
			p.Close()
		}
	}
}

// rolePools: the real server's connections, the clock `ahead` of now (0: now).
func rolePools(t *testing.T, ahead time.Duration) (app, authp *pgxpool.Pool) {
	t.Helper()
	if appDB == nil || authDB == nil {
		t.Fatalf("FIXTURE: the real server runs as argon_app and argon_auth, as in production, and this run has no such connections: " +
			"set ARGON_TEST_APP_URL, ARGON_TEST_AUTH_URL and ARGON_TEST_GRANTS (mutate.sh's GREEN and the drill do)")
	}
	if ahead == 0 {
		return appDB, authDB
	}
	return shifted(t, appDB, ahead), shifted(t, authDB, ahead)
}

// shifted: a copy of pool whose auth_now() reads `ahead` into the future —
// "the same server, later". Honoured only because the test database enables
// the test clock (TestAuth0).
func shifted(t *testing.T, pool *pgxpool.Pool, ahead time.Duration) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["argon.test_now"] = time.Now().Add(ahead).UTC().Format(time.RFC3339Nano)
	p, err := pgxpool.NewWithConfig(bg(), cfg)
	fixture(t, err, "a pool with the clock ahead")
	t.Cleanup(p.Close)
	return p
}
