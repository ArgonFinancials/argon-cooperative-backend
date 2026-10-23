//go:build tier1

// Package tier1 holds the acceptance tests for migration 0002. They are Tier 1
// (ledger posting, signatory quorum) and were written BEFORE the migration, to
// be reviewed by a human before any of it is implemented.
//
// Every test asserts a SPECIFIC refusal: SQLSTATE plus constraint (or column)
// name. A refusal for any other reason is "WRONG REFUSAL", not a pass. A
// failure while building a test's own fixtures is reported as "FIXTURE:" so
// the mutation run can tell a broken test from a red one.
//
// Run through test/tier1/mutate.sh (or `make tier1`). See test/tier1/README.md.
package tier1

import (
	"strconv"
	"context"
	"regexp"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var db *pgxpool.Pool

// adminURL and dbName: the scratch database and a superuser route to it, for
// tests that must change a DATABASE-level setting (TestAuth0_*).
var adminURL, dbName string

func TestMain(m *testing.M) { os.Exit(run(m)) }

func run(m *testing.M) int {
	ctx := context.Background()
	admin, mig, schema := os.Getenv("ARGON_TEST_ADMIN_URL"), os.Getenv("ARGON_TEST_MIGRATOR_URL"), os.Getenv("ARGON_TEST_SCHEMA")
	// ARGON_TEST_DB: run against an EXISTING scratch database (the restore
	// drill's restored copy) instead of building one from ARGON_TEST_SCHEMA.
	// Only a scratch name is accepted, never the live database, and it is
	// never dropped here: the drill owns it.
	existing := os.Getenv("ARGON_TEST_DB")
	// Fixtures name things from one per-process counter (member numbers,
	// phones, device tokens). In a fresh database that is enough; when several
	// test processes share ONE database (the restore drill runs every suite
	// against the same restored copy), each must start its own range, or the
	// second process re-creates the first one's identifiers and a collision
	// reads like a product defect (it did: a shared member number routed a
	// sign-in to D-21's ambiguous path, and the lockout "failed").
	var base int64
	if v := os.Getenv("ARGON_TEST_SEQ_BASE"); v != "" {
		var err error
		base, err = strconv.ParseInt(v, 10, 64)
		if err != nil || base < 0 || base > 99_000_000 {
			fmt.Fprintf(os.Stderr, "tier1: ARGON_TEST_SEQ_BASE=%q must be 0..99000000\n", v)
			os.Exit(2)
		}
		seq.Store(base)
	}
	if admin == "" || mig == "" || (schema == "" && existing == "") {
		// Not t.Skip: a skipped Tier 1 suite reads as a green one.
		fmt.Fprintln(os.Stderr, "tier1: set ARGON_TEST_ADMIN_URL, ARGON_TEST_MIGRATOR_URL, and ARGON_TEST_SCHEMA or ARGON_TEST_DB (see test/tier1/README.md)")
		return 2
	}
	if existing != "" && !regexp.MustCompile(`^argon_(drill|verify)_[0-9a-z_]+$`).MatchString(existing) {
		fmt.Fprintf(os.Stderr, "tier1: ARGON_TEST_DB=%q is not a scratch database; refusing (tests write rows that cannot be removed)\n", existing)
		return 2
	}

	name := existing
	if name == "" {
		// The time AND the process: Windows' clock ticks in 100 ns, and two
		// concurrent runs once made the same name in one tick (finding 38).
		name = fmt.Sprintf("argon_verify_t%d%07d", time.Now().UnixNano(), os.Getpid()%10000000)
	}
	adminURL, dbName = admin, name
	ac, err := pgx.Connect(ctx, admin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tier1: admin connect:", err)
		return 2
	}
	defer ac.Close(ctx)
	setup := []string{
		// The auth suite's clock. auth_now() honours argon.test_now only when
		// this DATABASE-level setting exists; a session-level SET does not count.
		`ALTER DATABASE ` + name + ` SET argon.test_clock = 'on'`,
	}
	if existing == "" {
		setup = append([]string{
			`CREATE DATABASE ` + name + ` OWNER argon_owner ENCODING 'UTF8' TEMPLATE template0`,
			`REVOKE ALL ON DATABASE ` + name + ` FROM PUBLIC`,
			`GRANT CONNECT ON DATABASE ` + name + ` TO argon_migrator`,
		}, setup...)
		// The drop is guarded like every destructive operation: only a name
		// this process generated, in the scratch pattern, is ever dropped.
		if !regexp.MustCompile(`^argon_verify_t[0-9]+$`).MatchString(name) {
			fmt.Fprintf(os.Stderr, "tier1: refusing to create or drop %q: not a scratch name\n", name)
			os.Exit(2)
		}
	}
	for i, q := range setup {
		if _, err := ac.Exec(ctx, q); err != nil {
			fmt.Fprintln(os.Stderr, "tier1:", q, err)
			return 2
		}
		// Armed only once THIS process's CREATE has succeeded. Armed before
		// it, a CREATE that failed on a name another process had just made
		// dropped that process's database out from under it (finding 38).
		if i == 0 && existing == "" {
			defer ac.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
		}
	}

	cfg, err := pgxpool.ParseConfig(mig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tier1: migrator url:", err)
		return 2
	}
	cfg.ConnConfig.Database = name
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tier1: pool:", err)
		return 2
	}
	defer db.Close()

	// Schema: 0001, then the 0002 interface (or 0002 itself), then at most one
	// mutant. Applied as argon_migrator, so objects are owned by argon_owner.
	// Skipped for an existing database: its schema is what is under test.
	for _, f := range strings.Split(schema, ",") {
		f = strings.TrimSpace(f)
		if existing != "" {
			break
		}
		// An empty entry once ended the list here, silently: every file after
		// it was never applied, and the run tested a schema nobody asked for.
		if f == "" {
			fmt.Fprintf(os.Stderr, "tier1: ARGON_TEST_SCHEMA has an empty entry (%q): refusing, since the files after it would not be applied\n", schema)
			return 2
		}
		sql, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tier1:", err)
			return 2
		}
		c, err := db.Acquire(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tier1:", err)
			return 2
		}
		_, err = c.Conn().PgConn().Exec(ctx, string(sql)).ReadAll()
		c.Release()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tier1: applying %s: %v\n", f, err)
			return 2
		}
	}
	// The real server's connections, as in production (cmd/api): the API as
	// argon_app, the auth service as argon_auth (findings 16 and 25, one layer
	// up). Made only where the database carries the real grants: grants.sql
	// applied here (ARGON_TEST_GRANTS, GREEN), or a restored copy (the drill).
	if code := openRolePools(ctx, ac, name, existing != ""); code != 0 {
		return code
	}
	defer closeRolePools()
	code := m.Run()
	// Each process owns [base, base + seqRange). Asserted, not assumed: a run
	// that used more would re-create the next range's member numbers and
	// phones, and that collision reads like a product defect.
	if used := seq.Load() - base; used >= seqRange {
		fmt.Fprintf(os.Stderr, "tier1: this run used %d fixture numbers, more than its range of %d: widen the range (deploy/lib.sh run_tier1)\n", used, seqRange)
		return 2
	}
	return code
}

// seqRange: the fixture numbers one test process may use. deploy/lib.sh's
// run_tier1 spaces the drill's suites this far apart; the phone fixtures have
// 8 digits, so 99 ranges fit.
const seqRange = 1_000_000

// ---- harness ----------------------------------------------------------------

func bg() context.Context { return context.Background() }

// tx runs fn in one transaction and commits it. The first error — from a
// statement, or from COMMIT, where deferred constraints fire — is returned.
// Tests do not care WHEN a rule fires, only that it does.
func tx(fn func(pgx.Tx) error) error {
	x, err := db.Begin(bg())
	if err != nil {
		return err
	}
	defer x.Rollback(bg())
	if err := fn(x); err != nil {
		return err
	}
	return x.Commit(bg())
}

func one(sql string, args ...any) error {
	return tx(func(x pgx.Tx) error { _, err := x.Exec(bg(), sql, args...); return err })
}

type want struct {
	codes      []string // acceptable SQLSTATEs
	constraint string   // required ConstraintName, if set
	column     string   // required ColumnName, if set
}

func (w want) String() string {
	return fmt.Sprintf("%v constraint=%q column=%q", w.codes, w.constraint, w.column)
}

func (w want) match(pe *pgconn.PgError) bool {
	ok := false
	for _, c := range w.codes {
		ok = ok || pe.Code == c
	}
	return ok && (w.constraint == "" || pe.ConstraintName == w.constraint) &&
		(w.column == "" || pe.ColumnName == w.column)
}

// refused passes only if err is a database error matching one of ws.
func refused(t *testing.T, err error, what string, ws ...want) {
	t.Helper()
	if err == nil {
		t.Fatalf("ACCEPTED — should have been refused: %s", what)
	}
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		t.Fatalf("WRONG REFUSAL — %s: not a database error: %v", what, err)
	}
	for _, w := range ws {
		if w.match(pe) {
			return
		}
	}
	t.Fatalf("WRONG REFUSAL — %s:\n  got  %s constraint=%q column=%q: %s\n  want one of %v",
		what, pe.Code, pe.ConstraintName, pe.ColumnName, pe.Message, ws)
}

func accepted(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("REFUSED — should have committed: %s: %v", what, err)
	}
}

// fixture marks setup the test depends on. Its failure means the TEST is
// broken for this schema, not that the schema is.
func fixture(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("FIXTURE: %s: %v", what, err)
	}
}

func nz(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// ---- fixtures ---------------------------------------------------------------

var seq atomic.Int64

type soc struct {
	t                            *testing.T
	id, branch, bank, savings    string
	actor                        string
	holderID                     string // see holder()
}

// newSociety gives each test its own society, so tests share a database but
// never each other's rows.
// Accounts carry a role once the schema has one (0004). The 0002 suite also
// runs on schemas without the column, so the fixture writes what the schema
// in use can hold — and, where it can, the role an account actually has.
var (
	roleOnce sync.Once
	hasRoles bool
)

func accountInsert(code, name, kind, role string) string {
	roleOnce.Do(func() {
		db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'accounts' AND column_name = 'role')`).Scan(&hasRoles)
	})
	if hasRoles {
		return fmt.Sprintf(`INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, '%s', '%s', '%s', '%s') RETURNING id`, code, name, kind, role)
	}
	return fmt.Sprintf(`INSERT INTO accounts (society_id, code, name, kind) VALUES ($1, '%s', '%s', '%s') RETURNING id`, code, name, kind)
}

func newSociety(t *testing.T) *soc {
	t.Helper()
	s := &soc{t: t}
	code := fmt.Sprintf("T%d-%d", time.Now().UnixNano()%1e9, seq.Add(1))
	fixture(t, tx(func(x pgx.Tx) error {
		steps := []struct {
			q    string
			args []any
			dst  *string
		}{
			{`INSERT INTO societies (code, registered_name, registration_number, state_directorate)
			  VALUES ($1, 'Test Cooperative', 'T/1', 'Lagos') RETURNING id`, []any{code}, &s.id},
			{`INSERT INTO branches (society_id, code, name) VALUES ($1, 'HQ', 'HQ') RETURNING id`, nil, &s.branch},
			{accountInsert("1000", "Bank", "asset", "bank"), nil, &s.bank},
			{accountInsert("2000", "Member savings", "liability", "member_savings"), nil, &s.savings},
			{`INSERT INTO users (full_name) VALUES ('Treasurer (actor)') RETURNING id`, []any{}, &s.actor},
		}
		for _, st := range steps {
			args := st.args
			if args == nil {
				args = []any{s.id}
			}
			if err := x.QueryRow(bg(), st.q, args...).Scan(st.dst); err != nil {
				return fmt.Errorf("%s: %w", strings.Fields(st.q)[2], err)
			}
		}
		return nil
	}), "society")
	// The test society has decided its withdrawals need two signatures, the
	// count its requests are raised at (0011: a withdrawal's quorum is the
	// society's, with no default). Its account floor stays undecided.
	var quorum bool
	fixture(t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public'
		AND table_name = 'societies' AND column_name = 'withdrawal_signatures_required')`).Scan(&quorum), "information_schema")
	if quorum {
		fixture(t, one(`UPDATE societies SET withdrawal_signatures_required = 2 WHERE id = $1`, s.id), "the withdrawal quorum")
	}
	return s
}

func (s *soc) user(name string) string {
	var id string
	fixture(s.t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ($1) RETURNING id`, name).Scan(&id), "user")
	return id
}

// member creates a member record, linked to a person (user) or not ("").
func (s *soc) member(user string) string {
	var id string
	fixture(s.t, db.QueryRow(bg(), `INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
		VALUES ($1, $2, $3, 'Member', $4, 'cash', '2020-01-01', $5) RETURNING id`,
		s.id, s.branch, fmt.Sprintf("M-%d", seq.Add(1)), fmt.Sprintf("+23470%08d", seq.Add(1)), nz(user)).Scan(&id), "member")
	return id
}

func (s *soc) appoint(member, post string, signatory bool, from, until string) error {
	return one(`INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from, held_until)
		VALUES ($1, $2, $3, $4, $5, $6)`, s.id, member, post, signatory, from, nz(until))
}

// signatory is a member record for user holding a CURRENT signing post. Every
// signature that should be accepted comes from one: 0002 refuses a signer who
// holds none (S1).
func (s *soc) signatory(user, post string) string {
	m := s.member(user)
	fixture(s.t, s.appoint(m, post, true, "2020-01-01", ""), "current "+post+" signatory appointment")
	return m
}

func (s *soc) request() string {
	// A withdrawal moves a member's savings: it names the member (0011), and
	// from 0016 who raised it, within what they may withdraw.
	id, err := s.withdrawalRequest(wreq{member: s.holder(), amount: 50000000, topUp: true})
	fixture(s.t, err, "approval request (2-of-3)")
	return id
}
// ---- withdrawal requests, in every era ---------------------------------------------

var (
	raisersOnce sync.Once
	raisers     bool
)

// hasRaisers: the schema records who raised a request (0016), read from the
// catalog (finding 34).
func hasRaisers() bool {
	raisersOnce.Do(func() {
		db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = 'public.approval_requests'::regclass
			AND attname = 'raised_by_user_id' AND NOT attisdropped)`).Scan(&raisers)
	})
	return raisers
}

// wreq: a withdrawal request a fixture raises.
//   raiser   "" the member's own user (from 0016 a request names its raiser)
//   expires  SQL for an expiry the TEST needs (an expired request, say); ""
//            the server's. From 0016 the server sets the expiry, so a fixture
//            that needs another one sets it afterwards, as the owner: test
//            scaffolding, never a path the app has.
//   topUp    from 0016 a request is within withdrawable when raised. Worlds
//            where money is not the subject (approvals, signatures) have the
//            member's savings topped up to cover it; the money tests never do,
//            so nothing adds money behind their backs.
type wreq struct {
	member, raiser, expires, resubmits, reference string
	amount                                         int64
	required                                       int
	topUp                                          bool
}

func (s *soc) withdrawalRequest(r wreq) (string, error) {
	if r.reference == "" {
		r.reference = fmt.Sprintf("APR-%d", seq.Add(1))
	}
	if r.required == 0 {
		r.required = 2
	}
	expires := r.expires
	if expires == "" {
		expires = `now() + interval '1 day'`
	}
	var id string
	if !hasRaisers() {
		// resubmits_id only when asked: the column is 0007's, and suites run
		// their breaks on schemas before it (suite 0002's failed on it).
		cols, vals, args := "", "", []any{s.id, r.reference, nz(r.member), r.amount, r.required}
		if r.resubmits != "" {
			cols, vals, args = ", resubmits_id", ", $6", append(args, r.resubmits)
		}
		err := db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at`+cols+`)
			VALUES ($1, $2, $3, 'withdrawal_out', $4, $5, `+expires+vals+`) RETURNING id`, args...).Scan(&id)
		return id, err
	}
	raiser := r.raiser
	if raiser == "" && r.member != "" {
		raiser = userOf(s.t, r.member)
	}
	if raiser == "" {
		s.t.Fatalf("FIXTURE: a withdrawal request for %q names no raiser: the member has no user, and no officer was named", r.member)
	}
	if r.topUp && r.member != "" {
		s.readyToWithdraw(r.member, r.amount)
		s.reachableQuorum(r.member, raiser, r.required)
	}
	proof := stepUp(s.t, raiser, "withdrawal_request")
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('withdrawal_request', $1, $2) RETURNING id`,
			raiser, proof).Scan(&act); err != nil {
			return fmt.Errorf("FIXTURE: the raiser's step-up act: %w", err)
		}
		return x.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at,
			  resubmits_id, raised_by_user_id, act_id)
			VALUES ($1, $2, $3, 'withdrawal_out', $4, $5, now() + interval '1 day', $6, $7, $8) RETURNING id`,
			s.id, r.reference, nz(r.member), r.amount, r.required, nz(r.resubmits), raiser, act).Scan(&id)
	})
	if err == nil && r.expires != "" {
		err = one(`UPDATE approval_requests SET expires_at = `+r.expires+`, raised_at = (`+r.expires+`) - interval '1 day' WHERE id = $1`, id)
	}
	return id, err
}

// reachableQuorum: from 0016 a request no quorum can approve is refused when
// raised. In worlds where money is not the subject, enough signatories who are
// neither the raiser nor the member are appointed to reach `required`: filler,
// labelled as such, who sign nothing unless a test makes them.
func (s *soc) reachableQuorum(member, raiser string, required int) {
	var eligible int
	fixture(s.t, db.QueryRow(bg(), `SELECT count(DISTINCT m.user_id) FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE a.society_id = $1 AND a.is_signatory AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)
		  AND m.user_id IS NOT NULL AND m.user_id IS DISTINCT FROM $2::uuid
		  AND m.user_id IS DISTINCT FROM (SELECT user_id FROM members WHERE id = $3)`, s.id, raiser, member).Scan(&eligible), "eligible signatories")
	// Appointments with an end date: one OPEN-ENDED holder per post
	// (appointments_one_current), so filler cannot take a post someone holds.
	for ; eligible < required; eligible++ {
		m := s.member(s.user("Fixture signatory (quorum only)"))
		fixture(s.t, s.appoint(m, "treasurer", true, "2020-01-01", "2099-12-31"), "a filler signatory")
	}
}

// readyToWithdraw: the society can quote and floor a withdrawal (a zero floor
// and zero charges, AS FIXTURE DATA, only where the society has set none),
// and the member's savings cover `amount` with its charges.
func (s *soc) readyToWithdraw(member string, amount int64) {
	fixture(s.t, one(`UPDATE societies SET account_floor_kobo = 0 WHERE id = $1 AND account_floor_kobo IS NULL`, s.id), "a zero floor (fixture data)")
	var series int
	fixture(s.t, db.QueryRow(bg(), `SELECT count(*) FROM charge_schedules WHERE society_id = $1 AND kind = 'withdrawal_out'`, s.id).Scan(&series), "the series")
	if series == 0 {
		// The four components, named outright: the first version derived them
		// from feeComponent(), which IS argon_margin from 0013, and dropped the
		// margin twice over (70 fixtures failed on "margin not set").
		for _, comp := range []string{"partner_fee", vatComponent(), "statutory_levy", "argon_margin"} {
			_, err := chargeSeries(s.id, "withdrawal_out", comp, "2020-01-01", "TEST DATA ONLY: zero, so a fixture's request can be raised; not a charge figure", band{})
			fixture(s.t, err, "a zero "+comp+" series (fixture data)")
		}
	}
	var short int64
	fixture(s.t, db.QueryRow(bg(), `SELECT greatest(0, $2 + (SELECT q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo
		  FROM charge_quote($3, 'withdrawal_out', $2, current_date) q)
		  + (SELECT account_floor_kobo FROM societies WHERE id = $3)
		  - (SELECT coalesce(-sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		      WHERE a.role = 'member_savings' AND l.member_id = $1))`, member, amount, s.id).Scan(&short), "what the member is short")
	if short > 0 {
		_, err := s.postEntry(entry{kind: "contribution_in", valueDate: "2026-01-02"},
			line{acct: s.bank, kobo: short}, line{acct: s.savings, kobo: -short, member: member})
		fixture(s.t, err, "savings to cover the request (fixture)")
	}
}


func (s *soc) sign(req, member, post string) error {
	return onStepUp(s.t, "signatory_approvals", "approval_signature", userOf(s.t, member),
		"request_id, member_id, post_at_signing", req, member, post)
}

// userOf: the person behind a member record, "" for none.
func userOf(t *testing.T, member string) string {
	var u *string
	fixture(t, db.QueryRow(bg(), `SELECT user_id FROM members WHERE id = $1`, member).Scan(&u), "the member's user")
	if u == nil {
		return ""
	}
	return *u
}

// onStepUp inserts one signature or rejection resting on `actor`'s own
// step-up act of `kind`, taken in the same transaction, where the schema has
// them (0010). Before 0010 a row names no act; nor does one by a member record
// with no person behind it, who can take no step-up. A fixture that carries
// its act is refused only by the rule its test is about.
func onStepUp(t *testing.T, table, kind, actor, cols string, args ...any) error {
	ph := func(n int) string {
		p := make([]string, n)
		for i := range p {
			p[i] = fmt.Sprintf("$%d", i+1)
		}
		return strings.Join(p, ", ")
	}
	var acts bool
	fixture(t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND column_name = 'act_id')`, table).Scan(&acts), "information_schema")
	if !acts || actor == "" {
		return one(fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, table, cols, ph(len(args))), args...)
	}
	proof := stepUp(t, actor, kind)
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
			kind, actor, proof).Scan(&act); err != nil {
			return fmt.Errorf("FIXTURE: the step-up act: %w", err)
		}
		_, err := x.Exec(bg(), fmt.Sprintf(`INSERT INTO %s (%s, act_id) VALUES (%s)`, table, cols, ph(len(args)+1)), append(args, act)...)
		return err
	})
}

type entry struct {
	kind, valueDate, postedAt, reverses, adjusts, reason, society, actor, batch string
	payoutRef                                                                  string // a payout names the transfer that paid it (0011); empty adds nothing
	unknownDate                                                                bool
}

func (s *soc) insertEntry(x pgx.Tx, e entry) (string, error) {
	kind, society, actor := e.kind, e.society, e.actor
	if kind == "" {
		kind = "contribution_in"
	}
	if society == "" {
		society = s.id
	}
	if actor == "" {
		actor = s.actor
	}
	var vd any = "2026-03-02"
	if e.valueDate != "" {
		vd = e.valueDate
	}
	if e.unknownDate {
		vd = nil
	}
	act, err := s.movementAct(x, e, kind, actor)
	if err != nil {
		return "", err
	}
	var id string
	cols := "society_id, batch_id, kind, value_date, posted_at, actor_user_id, actor_post, actor_post_label, reverses_id, adjusts_id, reason"
	vals := "$1, $2, $3, $4, coalesce($5::timestamptz, now()), $6, 'treasurer', 'Treasurer', $7, $8, $9"
	args := []any{society, nz(e.batch), kind, vd, nz(e.postedAt), actor, nz(e.reverses), nz(e.adjusts), nz(e.reason)}
	if act != "" {
		cols, vals, args = cols+", act_id", vals+fmt.Sprintf(", $%d", len(args)+1), append(args, act)
	}
	// From 0015 a single contribution names the client's reference for it.
	if e.batch == "" && e.reverses == "" && e.adjusts == "" {
		if cr := clientRefOf(kind, ""); cr != nil {
			cols, vals, args = cols+", client_reference", vals+fmt.Sprintf(", $%d", len(args)+1), append(args, cr)
		}
	}
	if e.payoutRef != "" {
		cols, vals, args = cols+", payout_reference", vals+fmt.Sprintf(", $%d", len(args)+1), append(args, e.payoutRef)
	}
	err = x.QueryRow(bg(), `INSERT INTO ledger_entries (`+cols+`) VALUES (`+vals+`) RETURNING id`, args...).Scan(&id)
	return id, err
}

var (
	movementActsOnce sync.Once
	hasMovementActs  bool
)

// movementAct: where the schema has them (0011), a single contribution or
// payout (no batch, not a correction) rests on its poster's own step-up act,
// written in the entry's transaction; the proof is minted in its own, as the
// app does. An actor who is no user can take no step-up: such an entry names
// none, and is refused for what it is (TestU5: the actor is no user). Returns
// "" where no act applies.
func (s *soc) movementAct(x pgx.Tx, e entry, kind, actor string) (string, error) {
	movementActsOnce.Do(func() {
		db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'ledger_entries' AND column_name = 'act_id')`).Scan(&hasMovementActs)
	})
	actKind := map[string]string{"contribution_in": "contribution_record", "withdrawal_out": "withdrawal_payout"}[kind]
	if !hasMovementActs || actKind == "" || e.batch != "" || e.reverses != "" || e.adjusts != "" {
		return "", nil
	}
	var isUser bool
	fixture(s.t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, actor).Scan(&isUser), "is the actor a user")
	if !isUser {
		return "", nil
	}
	proof := stepUp(s.t, actor, actKind)
	var act string
	if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
		actKind, actor, proof).Scan(&act); err != nil {
		return "", fmt.Errorf("FIXTURE: the poster's step-up act: %w", err)
	}
	return act, nil
}

type line struct {
	acct   string
	kobo   int64
	member string
	role   string
}

func (s *soc) insertLines(x pgx.Tx, entryID string, ls ...line) error {
	for _, l := range ls {
		role := l.role
		if role == "" {
			role = "principal"
		}
		if _, err := x.Exec(bg(), `INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id)
			VALUES ($1, $2, $3, $4, $5)`, entryID, l.acct, l.kobo, role, nz(l.member)); err != nil {
			return err
		}
	}
	return nil
}

// holder is the society's default account holder: the member whose savings
// the default entries move. A savings line that names no member says nothing
// about whose savings it is. 0004 refuses one, and this fixture was wrong
// before 0004 existed; nothing caught it.
func (s *soc) holder() string {
	if s.holderID == "" {
		// From 0016 the holder raises its own requests, so it is a person.
		user := ""
		if hasRaisers() {
			user = s.user("Holder")
		}
		s.holderID = s.member(user)
	}
	return s.holderID
}

func (s *soc) balanced() []line {
	return []line{{acct: s.bank, kobo: 10000}, {acct: s.savings, kobo: -10000, member: s.holder()}}
}

// postEntry writes an entry and its lines in one transaction.
func (s *soc) postEntry(e entry, ls ...line) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) (err error) {
		if id, err = s.insertEntry(x, e); err != nil {
			return err
		}
		return s.insertLines(x, id, ls...)
	})
	return id, err
}

// posted is a committed, balanced entry the test builds on.
func (s *soc) posted(e entry) string {
	id, err := s.postEntry(e, s.balanced()...)
	fixture(s.t, err, "posted entry")
	return id
}

func (s *soc) period(from, to string) string {
	var id string
	fixture(s.t, db.QueryRow(bg(), `INSERT INTO accounting_periods (society_id, starts_on, ends_on) VALUES ($1, $2, $3) RETURNING id`,
		s.id, from, to).Scan(&id), "period "+from)
	return id
}

func (s *soc) close(period string) {
	fixture(s.t, one(`UPDATE accounting_periods SET closed_at = now(), closed_by = $1 WHERE id = $2`, s.actor, period), "close period")
}

const noSuchRow = "00000000-0000-4000-8000-00000000dead"

// =============================================================================
// G1 — A POSTED ENTRY MUST NOT GROW (LEDGER_CONTRACT.md §2)
//
// The danger is that a BALANCED addition is invisible: the entry still sums to
// zero and the existing balance trigger passes it. An unbalanced addition
// proves nothing here — 0001 already refuses that.
// =============================================================================

var wEntryOpen = want{codes: []string{"23514"}, constraint: "ledger_lines_entry_open"}

func TestG1_BalancedPairOnPostedEntryIsRefused(t *testing.T) {
	s := newSociety(t)
	e := s.posted(entry{})
	err := tx(func(x pgx.Tx) error {
		return s.insertLines(x, e,
			line{acct: s.bank, kobo: 500},
			line{acct: s.savings, kobo: -500, member: s.holder()})
	})
	refused(t, err, "a balanced +500/−500 pair added in a later transaction to an already-posted entry", wEntryOpen)
}

// posted_at is a fact about when the SERVER wrote the row. A caller has no
// standing to assert it — accepting one lets the caller choose the immutability
// window (an entry dated 2099 stays open until 2099). Refused outright, in
// either direction.
var wPostedAtIsNow = want{codes: []string{"23514"}, constraint: "ledger_entries_posted_at_is_now"}

func TestG1_CallerCannotSupplyFuturePostedAt(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{postedAt: "2099-01-01T00:00:00Z"}, s.balanced()...)
	refused(t, err, "an entry whose caller supplied posted_at = 2099", wPostedAtIsNow)
}

func TestG1_CallerCannotSupplyPastPostedAt(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{postedAt: "2020-01-01T00:00:00Z"}, s.balanced()...)
	refused(t, err, "an entry whose caller supplied posted_at = 2020", wPostedAtIsNow)
}

// now() is the START of the current transaction. A transaction that began
// BEFORE an entry was committed has an earlier now() than that entry's
// posted_at — so "posted_at < now()" reads the entry as still open, and the
// older transaction can append to it. Server-generated posted_at does not close
// this; it is a race, not a forgery.
func TestG1_OlderTransactionCannotAppendToNewerEntry(t *testing.T) {
	s := newSociety(t)
	old, err := db.Begin(bg())
	fixture(t, err, "begin the older transaction")
	defer old.Rollback(bg())
	_, err = old.Exec(bg(), `SELECT now()`)
	fixture(t, err, "older transaction running")
	time.Sleep(20 * time.Millisecond)
	e := s.posted(entry{}) // committed by another connection, after `old` began
	err = s.insertLines(old, e, line{acct: s.bank, kobo: 500}, line{acct: s.savings, kobo: -500, member: s.holder()})
	if err == nil {
		err = old.Commit(bg())
	}
	refused(t, err, "a transaction that began before the entry was posted appends a balanced pair to it", wEntryOpen)
}

func TestG1_Control_BatchOfEntriesInOneTransaction(t *testing.T) {
	s := newSociety(t)
	err := tx(func(x pgx.Tx) error {
		a, err := s.insertEntry(x, entry{})
		if err != nil {
			return err
		}
		b, err := s.insertEntry(x, entry{})
		if err != nil {
			return err
		}
		// Lines for the FIRST entry after the second exists: postAll writes a
		// whole import in one transaction, in whatever order it builds it.
		if err := s.insertLines(x, a, s.balanced()...); err != nil {
			return err
		}
		return s.insertLines(x, b, s.balanced()...)
	})
	accepted(t, err, "two entries and their lines, interleaved, in one transaction (postAll)")
}

func TestG1_Control_EntryCreatedInsideSavepoint(t *testing.T) {
	s := newSociety(t)
	err := tx(func(x pgx.Tx) error {
		if _, err := x.Exec(bg(), `SAVEPOINT sp`); err != nil {
			return err
		}
		id, err := s.insertEntry(x, entry{})
		if err != nil {
			return err
		}
		if _, err := x.Exec(bg(), `RELEASE SAVEPOINT sp`); err != nil {
			return err
		}
		return s.insertLines(x, id, s.balanced()...)
	})
	accepted(t, err, "entry created inside a savepoint, lines added after RELEASE, one transaction")
}

// =============================================================================
// U — SIGNATURES ARE KEYED ON THE PERSON (PRD 8.4)
//
// The failure this exists for is an ACCIDENT, not fraud: the import creates a
// duplicate member record for one person, and that person then satisfies a
// 2-of-3 alone by signing once as each record.
// =============================================================================

var wOnePerUser = want{codes: []string{"23505"}, constraint: "signatory_approvals_one_per_user"}

func TestU1_OnePersonTwoMemberRecordsCannotSignTwice(t *testing.T) {
	s := newSociety(t)
	person := s.user("Adaeze Okafor")
	a := s.signatory(person, "treasurer") // the real record, holding the post
	b := s.member(person)                 // the migration duplicate
	req := s.request()
	fixture(t, s.sign(req, a, "treasurer"), "first signature, as member record A")
	refused(t, s.sign(req, b, "chairman"),
		"the same person signing the same 2-of-3 request again, as their duplicate member record B", wOnePerUser)
}

func TestU1_Control_DifferentPeopleSignSameRequest(t *testing.T) {
	s := newSociety(t)
	a, b := s.signatory(s.user("Treasurer"), "treasurer"), s.signatory(s.user("Chairman"), "chairman")
	req := s.request()
	fixture(t, s.sign(req, a, "treasurer"), "first signature")
	accepted(t, s.sign(req, b, "chairman"), "a second, different person signs the same request")
}

func TestU1_Control_SamePersonSignsDifferentRequests(t *testing.T) {
	s := newSociety(t)
	a := s.signatory(s.user("Treasurer"), "treasurer")
	r1, r2 := s.request(), s.request()
	fixture(t, s.sign(r1, a, "treasurer"), "signature on request 1")
	accepted(t, s.sign(r2, a, "treasurer"), "the same person signs a different request")
}

func TestU2_SuppliedUserMustBeTheMembersUser(t *testing.T) {
	s := newSociety(t)
	p, q := s.user("P"), s.user("Q")
	a := s.signatory(p, "treasurer")
	req := s.request()
	refused(t, onStepUp(t, "signatory_approvals", "approval_signature", p, "request_id, member_id, post_at_signing, user_id", req, a, "treasurer", q),
		"a signature by P's member record recorded as user Q",
		want{codes: []string{"23514"}, constraint: "signatory_user_is_member_user"})
}

func TestU2_Control_SuppliedMatchingUserIsAccepted(t *testing.T) {
	s := newSociety(t)
	p := s.user("P")
	a := s.signatory(p, "treasurer")
	req := s.request()
	accepted(t, onStepUp(t, "signatory_approvals", "approval_signature", p, "request_id, member_id, post_at_signing, user_id", req, a, "treasurer", p),
		"a signature whose supplied user matches the member record's user")
}

func TestU3_MemberWithoutUserCannotSign(t *testing.T) {
	s := newSociety(t)
	a := s.member("") // e.g. a cash member who has never logged in
	fixture(t, s.appoint(a, "treasurer", true, "2020-01-01", ""), "treasurer appointment on the unlinked record")
	req := s.request()
	refused(t, s.sign(req, a, "treasurer"), "a signature by a member record linked to no person",
		want{codes: []string{"23502"}, column: "user_id"},
		want{codes: []string{"23514"}, constraint: "signatory_has_user"})
}

// §4: who signed is frozen at signing. Relinking a member record afterwards
// must not re-attribute the signature. (Refusing the relink also satisfies
// this; the assertion is on the signature either way.)
func TestU4_SignatureUserIsFrozenAtSigning(t *testing.T) {
	s := newSociety(t)
	p, q := s.user("P"), s.user("Q")
	a := s.signatory(p, "treasurer")
	req := s.request()
	fixture(t, s.sign(req, a, "treasurer"), "signature by P")
	if err := one(`UPDATE members SET user_id = $1 WHERE id = $2`, q, a); err != nil {
		t.Logf("relink refused (%v)", err)
	}
	var got *string
	fixture(t, db.QueryRow(bg(), `SELECT user_id::text FROM signatory_approvals WHERE request_id = $1 AND member_id = $2`, req, a).Scan(&got), "read signature")
	if got == nil || *got != p {
		g := "NULL"
		if got != nil {
			g = *got
		}
		t.Fatalf("ACCEPTED — the signature now reads as user %s; it was signed by %s. Attribution must be frozen at signing (§4)", g, p)
	}
}

func TestU5_EntryActorMustBeAUser(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{actor: noSuchRow}, s.balanced()...)
	refused(t, err, "an entry whose actor_user_id is no user",
		want{codes: []string{"23503"}, constraint: "ledger_entries_actor_user_id_fkey"})
}

// =============================================================================
// G3 + G4 — THE INTERNAL AUDITOR IS READ-ONLY, BY EVERY ROUTE
//
// 0001's CHECK stops one route (an auditor appointment marked signatory). G3:
// the auditor signs anyway. G4: the auditor also holds a signing post. Both
// must hold for the PERSON, not just the member record.
// =============================================================================

var wNotAuditor = want{codes: []string{"23514"}, constraint: "signatory_not_auditor"}
var wAuditorNoPost = want{codes: []string{"23514", "23P01"}, constraint: "auditor_holds_no_signing_post"}

func TestG3_AuditorCannotSignUnderAuditorLabel(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Auditor"))
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", ""), "auditor appointment")
	refused(t, s.sign(s.request(), a, "internal_auditor"), "the current internal auditor signs as internal_auditor", wNotAuditor)
}

func TestG3_AuditorCannotSignUnderAnotherLabel(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Auditor"))
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", ""), "auditor appointment")
	refused(t, s.sign(s.request(), a, "treasurer"), "the current internal auditor signs, labelled as treasurer", wNotAuditor)
}

func TestG3_AuditorCannotSignThroughSecondMemberRecord(t *testing.T) {
	s := newSociety(t)
	person := s.user("Auditor")
	a, b := s.member(person), s.member(person)
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", ""), "auditor appointment on record A")
	refused(t, s.sign(s.request(), b, "treasurer"), "the current internal auditor signs through their other member record B", wNotAuditor)
}

func TestG3_Control_FormerAuditorMaySign(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Former auditor"))
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", "2020-12-31"), "auditor appointment that ended in 2020")
	fixture(t, s.appoint(a, "treasurer", true, "2021-01-01", ""), "treasurer from 2021")
	accepted(t, s.sign(s.request(), a, "treasurer"), "someone whose auditor term ended years ago signs")
}

func TestG4_AuditorThenSignatorySameMember(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("P"))
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", ""), "auditor appointment")
	refused(t, s.appoint(a, "treasurer", true, "2020-06-01", ""), "the current auditor is also appointed signatory treasurer", wAuditorNoPost)
}

func TestG4_SignatoryThenAuditorSameMember(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("P"))
	fixture(t, s.appoint(a, "treasurer", true, "2020-01-01", ""), "treasurer appointment")
	refused(t, s.appoint(a, "internal_auditor", false, "2020-06-01", ""), "the current signatory treasurer is also appointed auditor", wAuditorNoPost)
}

func TestG4_AuditorAndSignatoryAcrossTwoMemberRecords(t *testing.T) {
	s := newSociety(t)
	person := s.user("P")
	a, b := s.member(person), s.member(person)
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", ""), "auditor appointment on record A")
	refused(t, s.appoint(b, "treasurer", true, "2020-06-01", ""), "the same person is signatory treasurer through member record B", wAuditorNoPost)
}

func TestG4_ExtendingAuditorTermIntoSignatoryTerm(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("P"))
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", "2020-12-31"), "auditor term, 2020")
	fixture(t, s.appoint(a, "treasurer", true, "2021-01-01", ""), "treasurer term from 2021")
	refused(t, one(`UPDATE appointments SET held_until = NULL WHERE member_id = $1 AND post = 'internal_auditor'`, a),
		"re-opening the ended auditor term so it overlaps the treasurer term", wAuditorNoPost)
}

// Two member records of different people; one holds a signing post, the other
// is auditor. Relinking the auditor's record to the signatory's user makes one
// PERSON both — without touching an appointment.
func TestG4_RelinkingRecordCannotMakeAuditorASignatory(t *testing.T) {
	s := newSociety(t)
	p := s.user("P")
	s.signatory(p, "treasurer")
	b := s.member(s.user("Q"))
	fixture(t, s.appoint(b, "internal_auditor", false, "2020-01-01", ""), "auditor appointment on Q's record")
	refused(t, one(`UPDATE members SET user_id = $1 WHERE id = $2`, p, b),
		"relinking the auditor's member record to the person who is signatory treasurer", wAuditorNoPost)
}

func TestG4_Control_ConsecutiveTerms(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("P"))
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", "2020-12-31"), "auditor term, 2020")
	accepted(t, s.appoint(a, "treasurer", true, "2021-01-01", ""), "treasurer term starting after the auditor term ended")
}

func TestG4_Control_AuditorMayHoldNonSigningPost(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("P"))
	fixture(t, s.appoint(a, "internal_auditor", false, "2020-01-01", ""), "auditor appointment")
	accepted(t, s.appoint(a, "secretary", false, "2020-06-01", ""), "the auditor also holds a NON-signing post")
}

// =============================================================================
// S — ONLY A CURRENT SIGNATORY SIGNS (PRD 8.4), POINT IN TIME
//
// Without this, blocking the auditor (G3) is the ONLY thing between any user
// and a 3-of-4. "Current" is judged at the moment the server records the
// signature, and that moment is not the caller's to state.
// =============================================================================

var wHoldsSigningPost = want{codes: []string{"23514"}, constraint: "signatory_holds_signing_post"}
var wSignedAtIsNow = want{codes: []string{"23514"}, constraint: "signatory_approvals_signed_at_is_now"}

func TestS1_UserWithNoAppointmentCannotSign(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Ordinary member"))
	refused(t, s.sign(s.request(), a, "treasurer"), "a signature from a person holding no appointment at all", wHoldsSigningPost)
}

func TestS1_NonSigningPostCannotSign(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Secretary"))
	fixture(t, s.appoint(a, "secretary", false, "2020-01-01", ""), "non-signing secretary appointment")
	refused(t, s.sign(s.request(), a, "secretary"), "a signature from a person whose only post is non-signing", wHoldsSigningPost)
}

func TestS2_EndedAppointmentCannotSign(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Former treasurer"))
	fixture(t, s.appoint(a, "treasurer", true, "2020-01-01", "2020-12-31"), "treasurer term that ended in 2020")
	refused(t, s.sign(s.request(), a, "treasurer"), "a signature from a treasurer whose term ended before signing", wHoldsSigningPost)
}

func TestS2_NotYetStartedAppointmentCannotSign(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Incoming treasurer"))
	fixture(t, s.appoint(a, "treasurer", true, "2099-01-01", ""), "treasurer term starting in 2099")
	refused(t, s.sign(s.request(), a, "treasurer"), "a signature from a treasurer whose term has not begun", wHoldsSigningPost)
}

func TestS3_CallerCannotSupplySignedAt(t *testing.T) {
	s := newSociety(t)
	a := s.signatory(s.user("Treasurer"), "treasurer")
	refused(t, onStepUp(t, "signatory_approvals", "approval_signature", userOf(t, a), "request_id, member_id, post_at_signing, signed_at", s.request(), a, "treasurer", "2026-01-15"),
		"a current signatory's signature with a caller-supplied signed_at", wSignedAtIsNow)
}

// The attack the rule above exists for: an ex-treasurer back-dates the
// signature into their old term.
func TestS3_EndedSignatoryCannotBackdateIntoTheirTerm(t *testing.T) {
	s := newSociety(t)
	a := s.member(s.user("Former treasurer"))
	fixture(t, s.appoint(a, "treasurer", true, "2020-01-01", "2020-12-31"), "treasurer term that ended in 2020")
	refused(t, onStepUp(t, "signatory_approvals", "approval_signature", userOf(t, a), "request_id, member_id, post_at_signing, signed_at", s.request(), a, "treasurer", "2020-06-01"),
		"an ex-treasurer's signature back-dated to June 2020, inside their old term", wSignedAtIsNow, wHoldsSigningPost)
}

func TestS_Control_CurrentSignatorySigns(t *testing.T) {
	s := newSociety(t)
	a := s.signatory(s.user("Treasurer"), "treasurer")
	accepted(t, s.sign(s.request(), a, "treasurer"), "the current treasurer signs")
}

// =============================================================================
// V — AN UNKNOWN AS-AT DATE IS NULL, NEVER TODAY (§7b)
//
// value_date is the ONLY record of the as-at date. ledger_batches.as_of was a
// second record of the same fact and is dropped (V4), not constrained to agree.
// =============================================================================

func (s *soc) openingBatch() string {
	var id string
	ref := fmt.Sprintf("sha256:%d", seq.Add(1))
	if !schemaHasBatchActs() {
		fixture(s.t, db.QueryRow(bg(), `INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo)
			VALUES ($1, $2, $3, 1, 10000) RETURNING id`, s.id, ref, s.actor).Scan(&id), "opening-balance batch")
		return id
	}
	// 0005: a batch rests on its actor's step-up act, written in the batch's
	// transaction and spending a proof minted before it.
	proof := mintProof(s.t, s.actor)
	fixture(s.t, tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('ledger_batch', $1, $2) RETURNING id`,
			s.actor, proof).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(bg(), `INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo, authorised_by)
			VALUES ($1, $2, $3, 1, 10000, $4) RETURNING id`, s.id, ref, s.actor, act).Scan(&id)
	}), "opening-balance batch, on its step-up act")
	return id
}

var (
	batchActOnce sync.Once
	batchHasAct  bool
)

// schemaHasBatchActs: does a batch name its authorising act (0005)?
func schemaHasBatchActs() bool {
	batchActOnce.Do(func() {
		db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'ledger_batches' AND column_name = 'authorised_by')`).Scan(&batchHasAct)
	})
	return batchHasAct
}

func TestV4_BatchesCarryNoAsOfColumn(t *testing.T) {
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'ledger_batches' AND column_name = 'as_of'`).Scan(&n), "information_schema")
	if n != 0 {
		t.Fatalf("ACCEPTED — ledger_batches.as_of still exists: a second record of the as-at date that value_date already holds")
	}
}

func TestV1_OpeningBalanceWithUnknownAsOfCommits(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{kind: "opening_balance", batch: s.openingBatch(), unknownDate: true}, s.balanced()...)
	accepted(t, err, "an opening balance whose society records carry no as-at date (value_date NULL)")
}

func TestV2_UnknownValueDateStaysNull(t *testing.T) {
	s := newSociety(t)
	id, err := s.postEntry(entry{kind: "opening_balance", batch: s.openingBatch(), unknownDate: true}, s.balanced()...)
	accepted(t, err, "opening balance with unknown as-at date")
	var vd *string
	fixture(t, db.QueryRow(bg(), `SELECT value_date::text FROM ledger_entries WHERE id = $1`, id).Scan(&vd), "read value_date")
	if vd != nil {
		t.Fatalf("ACCEPTED — value_date became %s. An unknown as-at date was replaced with an invented one (§7b)", *vd)
	}
}

func TestV3_OnlyOpeningBalanceMayHaveUnknownDate(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{kind: "contribution_in", unknownDate: true}, s.balanced()...)
	refused(t, err, "a contribution with no value_date",
		want{codes: []string{"23514"}, constraint: "value_date_known_unless_opening_balance"},
		want{codes: []string{"23502"}, column: "value_date"})
}

func TestV_Control_OpeningBalanceWithKnownAsOf(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{kind: "opening_balance", batch: s.openingBatch(), valueDate: "2019-12-31"}, s.balanced()...)
	accepted(t, err, "an opening balance with a known as-at date")
}

// =============================================================================
// M — A MEMBER'S BALANCE IS A FACT ABOUT LINES
// =============================================================================

func TestM1_EntriesCarryNoMemberColumn(t *testing.T) {
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'ledger_entries' AND column_name = 'member_id'`).Scan(&n), "information_schema")
	if n != 0 {
		t.Fatalf("ACCEPTED — ledger_entries.member_id still exists: a second record of which member a line belongs to")
	}
}

func TestM2_MemberToMemberTransferInOneEntry(t *testing.T) {
	s := newSociety(t)
	x, y := s.member(""), s.member("")
	_, err := s.postEntry(entry{kind: "transfer_out"},
		line{acct: s.savings, kobo: 5000, member: x},
		line{acct: s.savings, kobo: -5000, member: y})
	accepted(t, err, "one entry moving ₦50 from member X's savings to member Y's")
	for m, wantSum := range map[string]int64{x: 5000, y: -5000} {
		var got int64
		fixture(t, db.QueryRow(bg(), `SELECT coalesce(sum(amount_kobo), 0) FROM ledger_lines WHERE member_id = $1`, m).Scan(&got), "member sum")
		if got != wantSum {
			t.Fatalf("member statement for %s sums to %d kobo, want %d", m, got, wantSum)
		}
	}
}

func TestM3_LineMemberMustExist(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{}, line{acct: s.bank, kobo: 10000}, line{acct: s.savings, kobo: -10000, member: noSuchRow})
	refused(t, err, "a savings line posted to a member that does not exist",
		want{codes: []string{"23503"}, constraint: "ledger_lines_member_id_fkey"})
}

func TestM4_LineMemberMustBelongToEntrysSociety(t *testing.T) {
	s, other := newSociety(t), newSociety(t)
	stranger := other.member("")
	_, err := s.postEntry(entry{}, line{acct: s.bank, kobo: 10000}, line{acct: s.savings, kobo: -10000, member: stranger})
	refused(t, err, "a line in society A's entry posted to a member of society B",
		want{codes: []string{"23514"}, constraint: "ledger_line_member_in_society"})
}

func TestM_Control_LinesWithoutMember(t *testing.T) {
	s := newSociety(t)
	x := s.member("")
	_, err := s.postEntry(entry{}, line{acct: s.bank, kobo: 10000}, line{acct: s.savings, kobo: -10000, member: x})
	accepted(t, err, "a contribution: the bank line belongs to no member, the savings line to member X")
}

// =============================================================================
// P — A CLOSED PERIOD IS READ-ONLY (§5)
// =============================================================================

var wPeriodClosed = want{codes: []string{"23514"}, constraint: "entry_not_in_closed_period"}
var wCloseFinal = want{codes: []string{"23514"}, constraint: "period_close_is_final"}

func TestP1_PostingIntoClosedPeriodIsRefused(t *testing.T) {
	s := newSociety(t)
	jan := s.period("2026-01-01", "2026-01-31")
	s.posted(entry{valueDate: "2026-01-10"})
	s.close(jan)
	// Posted today, dated inside January: the date that matters is value_date.
	_, err := s.postEntry(entry{valueDate: "2026-01-20"}, s.balanced()...)
	refused(t, err, "an entry dated 20 January after January was closed", wPeriodClosed)
}

func TestP2_Control_PostingIntoOpenPeriod(t *testing.T) {
	s := newSociety(t)
	s.close(s.period("2026-01-01", "2026-01-31"))
	s.period("2026-02-01", "2026-02-28")
	_, err := s.postEntry(entry{valueDate: "2026-02-10"}, s.balanced()...)
	accepted(t, err, "an entry dated in open February while January is closed")
}

func TestP3_ClosedPeriodCannotBeReopened(t *testing.T) {
	s := newSociety(t)
	jan := s.period("2026-01-01", "2026-01-31")
	s.close(jan)
	refused(t, one(`UPDATE accounting_periods SET closed_at = NULL WHERE id = $1`, jan),
		"re-opening a closed period (reopen, post, re-close would make close decorative)", wCloseFinal)
}

func TestP3_ClosedPeriodBoundariesCannotMove(t *testing.T) {
	s := newSociety(t)
	jan := s.period("2026-01-01", "2026-01-31")
	s.close(jan)
	refused(t, one(`UPDATE accounting_periods SET ends_on = '2026-01-15' WHERE id = $1`, jan),
		"shrinking a closed period so 16–31 January fall outside it", wCloseFinal)
}

// DELIBERATELY NOT TESTED: "a close is refused if its trial balance does not
// sum to zero" (§5). Every entry is already forced to balance at commit (0001,
// ledger_entry_balances), so an unbalanced trial balance cannot be constructed
// to test against. Such a test could never fail, and a permanently green test
// reads as evidence that it proves something. Do not add one. If the balance
// trigger is ever weakened, test THAT — see deploy/70-verify-tier1.sh, C3.

func TestP4_PeriodsInOneSocietyCannotOverlap(t *testing.T) {
	s := newSociety(t)
	s.period("2026-01-01", "2026-01-31")
	refused(t, one(`INSERT INTO accounting_periods (society_id, starts_on, ends_on) VALUES ($1, '2026-01-15', '2026-02-15')`, s.id),
		"a period overlapping another in the same society (a date both open and closed)",
		want{codes: []string{"23P01", "23514"}, constraint: "accounting_periods_no_overlap"})
}

func TestP4_Control_SameDatesInAnotherSociety(t *testing.T) {
	s, other := newSociety(t), newSociety(t)
	// Dates no other test uses: tests share a database, and an over-strict
	// (cross-society) rule must be caught on the statement below, not tripped
	// by another test's periods while building this fixture.
	s.period("2031-05-01", "2031-05-31")
	accepted(t, one(`INSERT INTO accounting_periods (society_id, starts_on, ends_on) VALUES ($1, '2031-05-01', '2031-05-31')`, other.id),
		"the same dates as a period in a different society")
}

// =============================================================================
// A — AN ADJUSTMENT REFERENCES WHAT IT CORRECTS (§2, §5)
// =============================================================================

const aReason = "Correction to a January contribution found after close"

func TestA1_AdjustmentOfClosedPeriodEntryPostsInOpenPeriod(t *testing.T) {
	s := newSociety(t)
	jan := s.period("2026-01-01", "2026-01-31")
	orig := s.posted(entry{valueDate: "2026-01-10"})
	s.close(jan)
	s.period("2026-02-01", "2026-02-28")
	_, err := s.postEntry(entry{valueDate: "2026-02-05", adjusts: orig, reason: aReason}, s.balanced()...)
	accepted(t, err, "an adjusting entry in open February referencing an entry in closed January (the §5 case)")
}

func TestA2_AdjustmentMustStateReason(t *testing.T) {
	s := newSociety(t)
	orig := s.posted(entry{})
	_, err := s.postEntry(entry{adjusts: orig}, s.balanced()...)
	refused(t, err, "an adjustment with no reason", want{codes: []string{"23514"}, constraint: "adjustment_states_reason"})
}

func TestA3_AdjustmentMustReferenceARealEntry(t *testing.T) {
	s := newSociety(t)
	_, err := s.postEntry(entry{adjusts: noSuchRow, reason: aReason}, s.balanced()...)
	refused(t, err, "an adjustment of an entry that does not exist",
		want{codes: []string{"23503"}, constraint: "ledger_entries_adjusts_id_fkey"})
}

func TestA4_CorrectionIsReversalOrAdjustmentNotBoth(t *testing.T) {
	s := newSociety(t)
	orig := s.posted(entry{})
	_, err := s.postEntry(entry{reverses: orig, adjusts: orig, reason: aReason}, s.balanced()...)
	refused(t, err, "an entry that is both a reversal and an adjustment",
		want{codes: []string{"23514"}, constraint: "correction_is_reversal_or_adjustment"})
}

func TestA5_AdjustmentMustBeInSameSociety(t *testing.T) {
	s, other := newSociety(t), newSociety(t)
	theirs := other.posted(entry{})
	_, err := s.postEntry(entry{adjusts: theirs, reason: aReason}, s.balanced()...)
	refused(t, err, "society A adjusting society B's entry",
		want{codes: []string{"23514"}, constraint: "correction_same_society"})
}
