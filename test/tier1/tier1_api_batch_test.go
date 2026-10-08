//go:build tier1

// ACCEPTANCE TESTS — postAll: POST /societies/{societyId}/ledger/batches.
// LEDGER_CONTRACT.md §1. Written before the handler, for review.
//
// The three things this call exists for, each proven by STATE, not by a
// status code:
//
//   - ATOMICITY. A failure partway through leaves nothing. The failure is a
//     real one, injected into the database at a chosen row AFTER earlier rows
//     have been written inside the transaction, so a handler cannot pass by
//     validating everything first and never failing partway.
//   - IDEMPOTENCY. A second call with the same reference writes nothing, is
//     not an error, and answers with what the FIRST commit did — its as-at
//     date included, read from the entries (0002 dropped it from the batch).
//   - A FAILED CALL DOES NOT CLAIM THE REFERENCE. After a failure, a refusal
//     or a client that gave up mid-call, a retry with the same key posts
//     everything exactly once.
//
// The client sends movements (member, amount), never accounts: the server
// builds each opening balance as migration_control Dr / member_savings Cr
// (D-01), choosing the accounts by role (0004).
//
// Which handler runs: default api.NewServer (internal/api, the real one);
// ARGON_TEST_API_FAKE=<break> runs the mutation vehicle in apifake_batch_test.go.
package tier1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/argon/backend/internal/auth"
)

// ---- the fixture: a society that can take a migration ---------------------------

type bworld struct {
	srv                  *httptest.Server
	s                    *soc
	migration            string
	secretary, secMember string
	members              []string
}

type worldOpt struct{ noMigration, twoSavings bool }

func newBatchWorld(t *testing.T, n int, opts ...worldOpt) *bworld {
	t.Helper()
	var o worldOpt
	if len(opts) > 0 {
		o = opts[0]
	}
	w := &bworld{srv: apiServer(t), s: newSociety(t)} // newSociety: one bank, one member_savings account
	if !o.noMigration {
		fixture(t, db.QueryRow(bg(), accountInsert("3900", "Migration control", "equity", "migration_control"), w.s.id).Scan(&w.migration), "migration control")
	}
	if o.twoSavings {
		var id string
		fixture(t, db.QueryRow(bg(), accountInsert("2010", "Target savings", "liability", "member_savings"), w.s.id).Scan(&id), "second savings account")
	}
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Secretary') RETURNING id`).Scan(&w.secretary), "secretary user")
	w.secMember = w.s.member(w.secretary)
	fixture(t, w.s.appoint(w.secMember, "secretary", true, "2020-01-01", ""), "secretary appointment")
	for i := 0; i < n; i++ {
		w.members = append(w.members, w.s.member(""))
	}
	return w
}

func (w *bworld) person(t *testing.T, post string, from, until string) string {
	t.Helper()
	var u string
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ($1) RETURNING id`, "Person "+post).Scan(&u), "user")
	m := w.s.member(u)
	if post != "" {
		fixture(t, w.s.appoint(m, post, post != "internal_auditor", from, until), post)
	}
	return u
}

type bRow struct {
	Line       int    `json:"line"`
	MemberId   string `json:"memberId"`
	AmountKobo any    `json:"amountKobo"`
}

type bBody struct {
	Kind               string  `json:"kind"`
	AsOf               any     `json:"asOf"`
	ConfirmedTotalKobo any     `json:"confirmedTotalKobo"`
	Rows               []bRow  `json:"rows"`
	PinProofId         *string `json:"pinProofId,omitempty"`
}

// proofKinds: does the schema scope proofs to an act (0005)? The fixtures
// mint what the schema in use can hold, as accountInsert does for roles.
var (
	proofKindOnce sync.Once
	proofHasKind  bool
)

// mintProof: a fresh PIN proof for user, minted as the auth service would —
// in its own transaction, before the call that spends it. A FIXTURE: it skips
// the PIN, which is the step-up suite's subject, not this one's.
func mintProof(t *testing.T, user string) string {
	t.Helper()
	var id string
	q := `INSERT INTO auth_proofs (user_id, method) VALUES ($1, 'pin') RETURNING id::text`
	if schemaHasProofKinds() {
		q = `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', 'ledger_batch') RETURNING id::text`
	}
	fixture(t, db.QueryRow(bg(), q, user).Scan(&id), "a PIN proof for the import")
	return id
}

func schemaHasProofKinds() bool {
	proofKindOnce.Do(func() {
		db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'auth_proofs' AND column_name = 'act_kind')`).Scan(&proofHasKind)
	})
	return proofHasKind
}

func (w *bworld) proof(t *testing.T) *string { p := mintProof(t, w.secretary); return &p }

// file builds the import of members[i] with amounts[i], file rows from 2 (row
// 1 is the header), confirming the true total.
func file(asOf any, members []string, amounts ...int64) bBody {
	b := bBody{Kind: "opening_balance", AsOf: asOf, Rows: []bRow{}}
	var total int64
	for i, a := range amounts {
		b.Rows = append(b.Rows, bRow{Line: i + 2, MemberId: members[i], AmountKobo: strconv.FormatInt(a, 10)})
		total += a
	}
	b.ConfirmedTotalKobo = strconv.FormatInt(total, 10)
	return b
}

// str prints an optional value as a reader would want it, not as a pointer.
func str(p *string) string {
	if p == nil {
		return "null"
	}
	return *p
}

func newKey() string {
	return fmt.Sprintf("IMPORT-%016x", seq.Add(1)*104729+time.Now().UnixNano()&0xffffff)
}

func postBatch(t *testing.T, c *http.Client, srv *httptest.Server, society, key, token string, body any, ctx ...context.Context) (resp, error) {
	t.Helper()
	raw, err := json.Marshal(body)
	fixture(t, err, "encode body")
	rc := bg()
	if len(ctx) > 0 {
		rc = ctx[0]
	}
	req, _ := http.NewRequestWithContext(rc, http.MethodPost, srv.URL+"/societies/"+society+"/ledger/batches", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := c.Do(req)
	if err != nil {
		return resp{}, err
	}
	defer r.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r.Body)
	return resp{r.StatusCode, buf.Bytes()}, nil
}

// post: as the secretary, with a fresh proof unless the body carries one.
func (w *bworld) post(t *testing.T, key string, body bBody) resp {
	t.Helper()
	if body.PinProofId == nil {
		body.PinProofId = w.proof(t)
	}
	r, err := postBatch(t, http.DefaultClient, w.srv, w.s.id, key, signIn(t, w.secretary), body)
	fixture(t, err, "POST batch")
	return r
}

type bRecord struct {
	Reference     string  `json:"reference"`
	Rows          int     `json:"rows"`
	TotalKobo     string  `json:"totalKobo"`
	AsOf          *string `json:"asOf"`
	PostedAt      string  `json:"postedAt"`
	AlreadyPosted bool    `json:"alreadyPosted"`
}

func record(t *testing.T, r resp, status int) bRecord {
	t.Helper()
	if r.status != status {
		t.Fatalf("WRONG STATUS — want %d, got %d: %s", status, r.status, r.body)
	}
	var rec bRecord
	if err := json.Unmarshal(r.body, &rec); err != nil {
		t.Fatalf("WRONG SHAPE — not a posted record: %v: %s", err, r.body)
	}
	return rec
}

type bRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Rows    []struct {
		Line     int    `json:"line"`
		MemberId string `json:"memberId"`
	} `json:"rows"`
}

func refusal(t *testing.T, r resp, status int, code string) bRefusal {
	t.Helper()
	if r.status != status {
		t.Fatalf("WRONG STATUS — want %d %s, got %d: %s", status, code, r.status, r.body)
	}
	var ref bRefusal
	if err := json.Unmarshal(r.body, &ref); err != nil {
		t.Fatalf("WRONG SHAPE — not a refusal: %v: %s", err, r.body)
	}
	if code != "" && ref.Code != code {
		t.Fatalf("WRONG REFUSAL — want code %q, got %q: %s", code, ref.Code, r.body)
	}
	return ref
}

// ---- what is in the ledger for a society --------------------------------------

type ledgerState struct{ batches, entries, lines int }

func stateOf(t *testing.T, society string) ledgerState {
	t.Helper()
	var s ledgerState
	fixture(t, db.QueryRow(bg(), `SELECT
		  (SELECT count(*) FROM ledger_batches WHERE society_id = $1),
		  (SELECT count(*) FROM ledger_entries WHERE society_id = $1),
		  (SELECT count(*) FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id WHERE e.society_id = $1)`,
		society).Scan(&s.batches, &s.entries, &s.lines), "ledger state")
	return s
}

func (s ledgerState) plus(b, e, l int) ledgerState {
	return ledgerState{s.batches + b, s.entries + e, s.lines + l}
}

func nothingWritten(t *testing.T, society string, before ledgerState, what string) {
	t.Helper()
	if after := stateOf(t, society); after != before {
		t.Fatalf("PARTIAL — %s, yet the ledger changed: batches %d→%d, entries %d→%d, lines %d→%d",
			what, before.batches, after.batches, before.entries, after.entries, before.lines, after.lines)
	}
}

// openingBalances: opening-balance entries crediting this member's savings.
func openingBalances(t *testing.T, member string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(DISTINCT e.id) FROM ledger_entries e
		JOIN ledger_lines l ON l.entry_id = e.id JOIN accounts a ON a.id = l.account_id
		WHERE e.kind = 'opening_balance' AND e.reverses_id IS NULL AND l.member_id = $1
		  AND a.role = 'member_savings' AND l.amount_kobo < 0`, member).Scan(&n), "opening balances")
	return n
}

func eachOnce(t *testing.T, members []string, what string) {
	t.Helper()
	for i, m := range members {
		if n := openingBalances(t, m); n != 1 {
			t.Fatalf("%s — row %d's member carries %d opening balances, want exactly 1", what, i+2, n)
		}
	}
}

// ---- fault injection: a real failure at a chosen row, inside the write --------
//
// A trigger on ledger_lines fails (or stalls) the line naming one member, and
// only that member, for this test only. Two sequences count, outside any
// transaction (nextval is never rolled back): how often the fault fired, and
// how many of this society's entries had been written when it did.

type fault struct {
	t                *testing.T
	name, hits, seen string
	removed          bool
}

func inject(t *testing.T, society, member, action string) *fault {
	t.Helper()
	n := seq.Add(1)
	f := &fault{t: t, name: fmt.Sprintf("tier1_fault_%d", n), hits: fmt.Sprintf("tier1_fault_hits_%d", n), seen: fmt.Sprintf("tier1_fault_seen_%d", n)}
	for _, q := range []string{
		`CREATE SEQUENCE ` + f.hits,
		`CREATE SEQUENCE ` + f.seen,
		fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger AS $f$ BEGIN
		   IF TG_TABLE_NAME = 'ledger_entries' THEN
		     IF NEW.society_id = '%s' THEN PERFORM nextval('%s'); END IF;
		   ELSIF NEW.member_id = '%s' THEN
		     PERFORM nextval('%s'); %s;
		   END IF;
		   RETURN NEW; END $f$ LANGUAGE plpgsql
		   -- The instrument runs with its creator's rights: the real server
		   -- writes as argon_app (roles_test.go), which is granted nothing on
		   -- a test's counters, and should not be.
		   SECURITY DEFINER SET search_path = public, pg_temp`, f.name, society, f.seen, member, f.hits, action),
		fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION %s()`, f.name, f.name),
		fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION %s()`, f.name, f.name),
	} {
		_, err := db.Exec(bg(), q)
		fixture(t, err, "inject fault")
	}
	t.Cleanup(f.remove)
	return f
}

const (
	raiseFault = `RAISE EXCEPTION 'tier1: injected fault' USING ERRCODE = 'XX000'`
	stall      = `PERFORM pg_sleep(1.5)`
)

func (f *fault) count(seqName string) int64 {
	var v int64
	var called bool
	fixture(f.t, db.QueryRow(bg(), `SELECT last_value, is_called FROM `+seqName).Scan(&v, &called), "fault counter")
	if !called {
		return 0
	}
	return v
}

func (f *fault) remove() {
	if f.removed {
		return
	}
	f.removed = true
	for _, q := range []string{
		`DROP TRIGGER IF EXISTS ` + f.name + ` ON ledger_lines`,
		`DROP TRIGGER IF EXISTS ` + f.name + ` ON ledger_entries`,
		`DROP FUNCTION IF EXISTS ` + f.name + `()`,
		`DROP SEQUENCE IF EXISTS ` + f.hits,
		`DROP SEQUENCE IF EXISTS ` + f.seen,
	} {
		db.Exec(bg(), q)
	}
}

// settled waits until no other session in this database is mid-statement, so
// a request the client abandoned has finished whatever it was going to do.
func settled(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var busy int
		fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend' AND state <> 'idle'`).Scan(&busy), "pg_stat_activity")
		if busy == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("FIXTURE: %d sessions still busy after 15s", busy)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stalled reports, without failing the test (it is polled from goroutines),
// whether the stall has been reached: the stalled line's write has begun.
func (f *fault) stalled() bool {
	var v int64
	var called bool
	if err := db.QueryRow(bg(), `SELECT last_value, is_called FROM `+f.hits).Scan(&v, &called); err != nil {
		return false
	}
	return called && v > 0
}

// whenStalled blocks until the stall is reached (true), or 10s pass (false).
// The fixtures gate on THIS, never on a head start measured by the clock: a
// slow moment must not decide whether the race being tested happened at all.
func (f *fault) whenStalled() bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if f.stalled() {
			return true
		}
	}
	return false
}

// race runs a, and b once a is inside its write (stalled), and reports whether
// a was still in flight when b began — without that, the race never happened.
func race(a, b func() resp, f *fault) (ra, rb resp, overlapped bool) {
	var wg sync.WaitGroup
	var aDone, bStart time.Time
	wg.Add(2)
	go func() { defer wg.Done(); ra = a(); aDone = time.Now() }()
	go func() { defer wg.Done(); f.whenStalled(); bStart = time.Now(); rb = b() }()
	wg.Wait()
	return ra, rb, aDone.After(bStart)
}

// =============================================================================
// WHAT A COMMIT WRITES
// =============================================================================

func TestApiBatch01_CommitsEachRowAsABalancedOpeningBalance(t *testing.T) {
	w := newBatchWorld(t, 3)
	key := newKey()
	amounts := []int64{4800000, 1250000, 99}
	rec := record(t, w.post(t, key, file("2025-12-31", w.members, amounts...)), http.StatusCreated)
	if rec.Reference != key || rec.Rows != 3 || rec.TotalKobo != "6050099" || rec.AsOf == nil || *rec.AsOf != "2025-12-31" || rec.AlreadyPosted {
		t.Fatalf("WRONG RECORD — %+v; want reference %s, 3 rows, total 6050099, asOf 2025-12-31, alreadyPosted false", rec, key)
	}

	var batch, actor string
	var rows int
	var total int64
	fixture(t, db.QueryRow(bg(), `SELECT id::text, actor_user_id::text, row_count, total_kobo FROM ledger_batches
		WHERE society_id = $1 AND reference = $2`, w.s.id, key).Scan(&batch, &actor, &rows, &total), "the batch row")
	if actor != w.secretary || rows != 3 || total != 6050099 {
		t.Fatalf("WRONG BATCH — actor %s, rows %d, total %d; want the secretary, 3, 6050099", actor, rows, total)
	}
	for i, m := range w.members {
		var (
			kind, eActor, label string
			post, valueDate     *string
			src                 *int
			n                   int
			mig, sav            int64
			savMember           *string
		)
		err := db.QueryRow(bg(), `SELECT e.kind::text, e.actor_user_id::text, e.actor_post::text, e.actor_post_label,
			       e.value_date::text, e.source_line,
			       (SELECT count(*) FROM ledger_lines x WHERE x.entry_id = e.id),
			       (SELECT coalesce(sum(x.amount_kobo), 0) FROM ledger_lines x WHERE x.entry_id = e.id AND x.account_id = $3 AND x.member_id IS NULL),
			       (SELECT coalesce(sum(x.amount_kobo), 0) FROM ledger_lines x JOIN accounts a ON a.id = x.account_id
			         WHERE x.entry_id = e.id AND a.role = 'member_savings'),
			       (SELECT max(x.member_id::text) FROM ledger_lines x JOIN accounts a ON a.id = x.account_id
			         WHERE x.entry_id = e.id AND a.role = 'member_savings')
			  FROM ledger_entries e
			 WHERE e.batch_id = $1 AND EXISTS (SELECT 1 FROM ledger_lines x WHERE x.entry_id = e.id AND x.member_id = $2)`,
			batch, m, w.migration).Scan(&kind, &eActor, &post, &label, &valueDate, &src, &n, &mig, &sav, &savMember)
		if err != nil {
			t.Fatalf("MISSING — row %d's member has no entry in the batch: %v", i+2, err)
		}
		switch {
		case kind != "opening_balance":
			t.Fatalf("WRONG KIND — row %d posted as %s", i+2, kind)
		case n != 2 || mig != amounts[i] || sav != -amounts[i] || savMember == nil || *savMember != m:
			t.Fatalf("WRONG ENTRY — row %d: %d lines, migration control %d, member savings %d (member %s); want 2 lines, Dr %d migration control, Cr %d to THIS member's savings (D-01)",
				i+2, n, mig, sav, str(savMember), amounts[i], amounts[i])
		case eActor != w.secretary || post == nil || *post != "secretary" || label != "Secretary":
			t.Fatalf("UNATTRIBUTED — row %d: actor %s, post %s, label %q; want the secretary, snapshotted (§4)", i+2, eActor, str(post), label)
		case valueDate == nil || *valueDate != "2025-12-31":
			t.Fatalf("WRONG DATE — row %d carries value date %s; want the society's as-at date 2025-12-31", i+2, str(valueDate))
		case src == nil || *src != i+2:
			got := "null"
			if src != nil {
				got = strconv.Itoa(*src)
			}
			t.Fatalf("UNTRACEABLE — row %d's entry records source line %s", i+2, got)
		}
	}
}

func TestApiBatch02_UnknownAsOfIsStoredAsNull(t *testing.T) {
	w := newBatchWorld(t, 2)
	key := newKey()
	rec := record(t, w.post(t, key, file(nil, w.members, 100000, 200000)), http.StatusCreated)
	if rec.AsOf != nil {
		t.Fatalf("INVENTED DATE — an unknown as-at date came back as %q", *rec.AsOf)
	}
	var dated int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM ledger_entries e JOIN ledger_batches b ON b.id = e.batch_id
		WHERE b.society_id = $1 AND b.reference = $2 AND e.value_date IS NOT NULL`, w.s.id, key).Scan(&dated), "dated entries")
	if dated != 0 {
		t.Fatalf("INVENTED DATE — %d entries of an import with no as-at date carry one (the import date, presumably)", dated)
	}
}

// =============================================================================
// ATOMICITY — and a failure does not claim the reference
// =============================================================================

func TestApiBatch03_FailurePartwayWritesNothing(t *testing.T) {
	w := newBatchWorld(t, 5)
	f := inject(t, w.s.id, w.members[3], raiseFault) // row 5 of the file, the 4th member
	before := stateOf(t, w.s.id)
	r := w.post(t, newKey(), file("2025-12-31", w.members, 100, 200, 300, 400, 500))

	if f.count(f.hits) == 0 {
		t.Fatalf("FIXTURE: the injected fault never fired (%d %s) — the call did not reach the 4th row's write, so this proves nothing", r.status, r.body)
	}
	if f.count(f.seen) == 0 {
		t.Fatalf("FIXTURE: no entry was written before the fault — not a failure PARTWAY")
	}
	nothingWritten(t, w.s.id, before, fmt.Sprintf("the write failed at the 4th row after %d entries were written in it", f.count(f.seen)))
	for i, m := range w.members {
		if n := openingBalances(t, m); n != 0 {
			t.Fatalf("PARTIAL — after the failure, row %d's member carries %d opening balance(s)", i+2, n)
		}
	}
	if ref := refusal(t, r, http.StatusInternalServerError, "post_failed"); ref.Message == "" {
		t.Fatalf("SILENT — the failure says nothing about what was written")
	}
}

func TestApiBatch04_RetryAfterFailureSucceeds(t *testing.T) {
	w := newBatchWorld(t, 5)
	f := inject(t, w.s.id, w.members[3], raiseFault)
	key, body := newKey(), file("2025-12-31", w.members, 100, 200, 300, 400, 500)
	before := stateOf(t, w.s.id)
	first := w.post(t, key, body)
	if f.count(f.hits) == 0 || first.status/100 == 2 {
		t.Fatalf("FIXTURE: the first call was meant to fail at the 4th row; got %d (fault fired %d times)", first.status, f.count(f.hits))
	}
	f.remove() // the cause goes away, as a timeout does

	rec := record(t, w.post(t, key, body), http.StatusCreated)
	if rec.AlreadyPosted || rec.Rows != 5 || rec.TotalKobo != "1500" {
		t.Fatalf("CLAIMED — the retry after a failure was answered as %+v; want a fresh commit of 5 rows, 1500", rec)
	}
	if after := stateOf(t, w.s.id); after != before.plus(1, 5, 10) {
		t.Fatalf("WRONG WRITE — the retry left %+v, want %+v", after, before.plus(1, 5, 10))
	}
	eachOnce(t, w.members, "WRONG WRITE")
}

// LIMIT: the break no_idempotency (a repeat attempts to post again) goes red
// here as a REFUSAL, not as a double posting — member_already_has_balance, the
// total check and the unique (society, reference) key all stand in its way. So
// these tests prove the ANSWER a repeat gets (200, the first commit's record),
// which is what the client acts on; that a repeat cannot post twice is proven
// by test 7 (racing) and test 10, not by the key alone.
func TestApiBatch05_SecondCallWritesNothingAndReturnsTheFirstCommit(t *testing.T) {
	w := newBatchWorld(t, 3)
	key := newKey()
	first := record(t, w.post(t, key, file("2025-12-31", w.members, 1000, 2000, 3000)), http.StatusCreated)
	before := stateOf(t, w.s.id)

	// The repeat says something else entirely — another as-at date, fewer rows,
	// a total that does not even match its rows. None of it matters: the
	// reference was committed, so nothing is judged and nothing is written.
	again := file("2026-03-31", w.members, 7)
	again.ConfirmedTotalKobo = "999"
	rec := record(t, w.post(t, key, again), http.StatusOK)

	nothingWritten(t, w.s.id, before, "the reference was already committed")
	switch {
	case !rec.AlreadyPosted:
		t.Fatalf("UNTOLD — the repeat did not say it was already posted: %+v", rec)
	case rec.AsOf == nil || *rec.AsOf != "2025-12-31":
		t.Fatalf("WRONG DATE — the repeat says the balances are as at %s; the first commit posted them as at 2025-12-31", str(rec.AsOf))
	case rec.Rows != 3 || rec.TotalKobo != "6000":
		t.Fatalf("WRONG RECORD — the repeat reports %d rows, %s; the first commit posted 3 rows, 6000", rec.Rows, rec.TotalKobo)
	case rec.PostedAt != first.PostedAt || rec.Reference != key:
		t.Fatalf("WRONG RECORD — posted at %s / %s; the first commit was %s / %s", rec.PostedAt, rec.Reference, first.PostedAt, key)
	}
	eachOnce(t, w.members, "POSTED TWICE")
}

func TestApiBatch06_SecondCallReturnsTheFirstCommitsUnknownAsOf(t *testing.T) {
	w := newBatchWorld(t, 2)
	key := newKey()
	record(t, w.post(t, key, file(nil, w.members, 1000, 2000)), http.StatusCreated)
	rec := record(t, w.post(t, key, file("2026-01-31", w.members, 1000, 2000)), http.StatusOK)
	if rec.AsOf != nil {
		t.Fatalf("WRONG DATE — the first commit carried no as-at date; the repeat says %q", *rec.AsOf)
	}
}

// A client that times out retries while its first request is still running.
func TestApiBatch07_RetryRacingTheOriginalPostsOnce(t *testing.T) {
	w := newBatchWorld(t, 3)
	f := inject(t, w.s.id, w.members[2], stall)
	key, body := newKey(), file("2025-12-31", w.members, 10, 20, 30)
	body.PinProofId = w.proof(t)
	tokA, tokB := signIn(t, w.secretary), signIn(t, w.secretary)
	before := stateOf(t, w.s.id)
	call := func(tok string) func() resp {
		return func() resp {
			r, err := postBatch(t, http.DefaultClient, w.srv, w.s.id, key, tok, body)
			if err != nil {
				return resp{status: -1, body: []byte(err.Error())}
			}
			return r
		}
	}
	a, b, overlapped := race(call(tokA), call(tokB), f)
	if !overlapped || f.count(f.hits) == 0 {
		t.Fatalf("FIXTURE: the second call did not start while the first was writing (overlap %v, stalls %d)", overlapped, f.count(f.hits))
	}
	statuses := map[int]int{a.status: 1}
	statuses[b.status]++
	if statuses[http.StatusCreated] != 1 || statuses[http.StatusOK] != 1 {
		t.Fatalf("WRONG ANSWERS — two calls with one reference answered %d and %d (%s / %s); want one 201 and one 200",
			a.status, b.status, a.body, b.body)
	}
	ra, rb := a, b
	if a.status == http.StatusOK {
		ra, rb = b, a
	}
	created, repeat := record(t, ra, http.StatusCreated), record(t, rb, http.StatusOK)
	if repeat.PostedAt != created.PostedAt || repeat.Rows != 3 {
		t.Fatalf("WRONG RECORD — the repeat reports %+v; the commit was %+v", repeat, created)
	}
	if after := stateOf(t, w.s.id); after != before.plus(1, 3, 6) {
		t.Fatalf("POSTED TWICE — %+v after, want %+v", after, before.plus(1, 3, 6))
	}
	eachOnce(t, w.members, "POSTED TWICE")
}

// The case the key exists for: one network timeout must not become a society
// that can never post its opening balances.
func TestApiBatch08_ClientTimeoutThenRetryPostsExactlyOnce(t *testing.T) {
	w := newBatchWorld(t, 3)
	f := inject(t, w.s.id, w.members[1], stall)
	key, body := newKey(), file("2025-12-31", w.members, 10, 20, 30)
	body.PinProofId = w.proof(t) // the retry carries the SAME proof: the abandoned call must not have spent it
	before := stateOf(t, w.s.id)

	// The client gives up once its request is inside the write — not after a
	// fixed time, which a slow moment could spend before the write began.
	ctx, giveUp := context.WithCancel(bg())
	go func() { f.whenStalled(); giveUp() }()
	if r, err := postBatch(t, &http.Client{}, w.srv, w.s.id, key, signIn(t, w.secretary), body, ctx); err == nil {
		t.Fatalf("FIXTURE: the client was meant to give up mid-call; it got %d: %s", r.status, r.body)
	}
	giveUp()
	settled(t) // whatever the abandoned request was going to do, it has done
	if f.count(f.hits) == 0 {
		t.Fatalf("FIXTURE: the abandoned call never reached the write")
	}
	f.remove()

	r := w.post(t, key, body)
	switch r.status {
	case http.StatusCreated: // the abandoned call wrote nothing; this one did
	case http.StatusOK: // the abandoned call committed after all; this is its record
	default:
		t.Fatalf("STUCK — the retry after a timeout answered %d: %s", r.status, r.body)
	}
	rec := record(t, r, r.status)
	if rec.Rows != 3 || rec.TotalKobo != "60" {
		t.Fatalf("CLAIMED — the retry reports %d rows, %s: a reference recorded without its entries", rec.Rows, rec.TotalKobo)
	}
	if after := stateOf(t, w.s.id); after.entries != before.entries+3 || after.lines != before.lines+6 {
		t.Fatalf("WRONG WRITE — after the timeout and a retry: %+v, want %+v", after, before.plus(1, 3, 6))
	}
	eachOnce(t, w.members, "WRONG WRITE")
}

// Two DIFFERENT files naming one member, committed at the same moment. The
// reference cannot catch this — different files, different keys.
func TestApiBatch09_TwoImportsNamingOneMemberCannotBothPost(t *testing.T) {
	w := newBatchWorld(t, 3)
	shared := w.members[1]
	f := inject(t, w.s.id, shared, stall)
	tokA, tokB := signIn(t, w.secretary), signIn(t, w.secretary)
	fileA := file("2025-12-31", w.members[0:2], 10, 20)
	fileB := file("2025-12-31", w.members[1:3], 30, 40)
	fileA.PinProofId, fileB.PinProofId = w.proof(t), w.proof(t)
	call := func(tok string, body bBody) func() resp {
		key := newKey()
		return func() resp {
			r, err := postBatch(t, http.DefaultClient, w.srv, w.s.id, key, tok, body)
			if err != nil {
				return resp{status: -1, body: []byte(err.Error())}
			}
			return r
		}
	}
	a, b, overlapped := race(call(tokA, fileA), call(tokB, fileB), f)
	if !overlapped || f.count(f.hits) == 0 {
		t.Fatalf("FIXTURE: the second import did not start while the first was writing (overlap %v)", overlapped)
	}
	if n := openingBalances(t, shared); n != 1 {
		t.Fatalf("POSTED TWICE — the member in both files carries %d opening balances (answers %d, %d)", n, a.status, b.status)
	}
	loser := b
	if a.status != http.StatusCreated {
		loser = a
	}
	if (a.status == http.StatusCreated) == (b.status == http.StatusCreated) {
		t.Fatalf("WRONG ANSWERS — %d and %d; want exactly one 201", a.status, b.status)
	}
	ref := refusal(t, loser, http.StatusUnprocessableEntity, "member_already_has_balance")
	if len(ref.Rows) != 1 || ref.Rows[0].MemberId != shared {
		t.Fatalf("UNNAMED — the refusal does not name the member already posted: %+v", ref.Rows)
	}
}

// =============================================================================
// REFUSALS — each writes nothing
// =============================================================================

// postOpening posts an opening balance directly, as an earlier import would have.
func (w *bworld) postOpening(t *testing.T, member string, kobo int64) string {
	t.Helper()
	id, err := w.s.postEntry(entry{kind: "opening_balance", unknownDate: true, actor: w.secretary},
		line{acct: w.migration, kobo: kobo}, line{acct: w.s.savings, kobo: -kobo, member: member})
	fixture(t, err, "an earlier opening balance")
	return id
}

func TestApiBatch10_MemberWithAnOpeningBalanceRefusesTheWholeFile(t *testing.T) {
	w := newBatchWorld(t, 3)
	w.postOpening(t, w.members[1], 50000)
	before := stateOf(t, w.s.id)
	ref := refusal(t, w.post(t, newKey(), file("2025-12-31", w.members, 10, 20, 30)), http.StatusUnprocessableEntity, "member_already_has_balance")
	if len(ref.Rows) != 1 || ref.Rows[0].Line != 3 || ref.Rows[0].MemberId != w.members[1] {
		t.Fatalf("UNNAMED — want line 3 (%s) named, got %+v", w.members[1], ref.Rows)
	}
	nothingWritten(t, w.s.id, before, "the file was refused — skipping the clash would be a partial migration by another name")
}

// The correction route the frontend gives an admin: reverse, then re-import.
func TestApiBatch11_ReversedOpeningBalanceDoesNotBlockAReimport(t *testing.T) {
	w := newBatchWorld(t, 1)
	m := w.members[0]
	orig := w.postOpening(t, m, 50000)
	_, err := w.s.postEntry(entry{kind: "opening_balance", unknownDate: true, actor: w.secretary, reverses: orig, reason: "Imported against the wrong member"},
		line{acct: w.s.savings, kobo: 50000, member: m}, line{acct: w.migration, kobo: -50000})
	fixture(t, err, "the reversal")
	record(t, w.post(t, newKey(), file("2025-12-31", w.members, 45000)), http.StatusCreated)
}

// A wrong total on a FIRST call: refused, nothing written. confirmedTotalKobo
// is the figure the secretary typed against the per-branch subtotals; if the
// server accepted one that disagrees with the rows, the secretary would
// confirm one figure and another would post. Then the refusal must not have
// claimed the key: the corrected retry, same file, same key, commits (201).
func TestApiBatch12_TotalMismatchRefusesAndDoesNotClaimTheReference(t *testing.T) {
	w := newBatchWorld(t, 2)
	key := newKey()
	body := file("2025-12-31", w.members, 1000, 2000)
	body.ConfirmedTotalKobo = "3001"
	before := stateOf(t, w.s.id)
	refusal(t, w.post(t, key, body), http.StatusUnprocessableEntity, "total_mismatch")
	nothingWritten(t, w.s.id, before, "the confirmed total did not match the rows")

	// Previewed again, the right figure confirmed: the same file, the same key.
	body.ConfirmedTotalKobo = "3000"
	if rec := record(t, w.post(t, key, body), http.StatusCreated); rec.AlreadyPosted || rec.Rows != 2 {
		t.Fatalf("CLAIMED — the refused reference was answered as %+v", rec)
	}
}

func TestApiBatch13_DuplicateMemberInTheFile(t *testing.T) {
	w := newBatchWorld(t, 2)
	body := file("2025-12-31", []string{w.members[0], w.members[1], w.members[0]}, 10, 20, 30)
	before := stateOf(t, w.s.id)
	ref := refusal(t, w.post(t, newKey(), body), http.StatusUnprocessableEntity, "duplicate_member")
	if len(ref.Rows) != 1 || ref.Rows[0].Line != 4 {
		t.Fatalf("UNNAMED — want the repeat at line 4 named, got %+v", ref.Rows)
	}
	nothingWritten(t, w.s.id, before, "one member appeared twice")
}

// LIMIT, stated where it will be read: a member of ANOTHER society is also
// refused by the schema (ledger_line_member_in_society, 0002), so the break
// foreign_member_ok goes red here through the schema's 500, not through the
// handler. The mutation run therefore does not show WHICH layer caught it.
// The test still pins the handler: it wants a 422 unknown_member naming the
// line, which only the handler gives, so removing the handler's check turns
// it red (500), never green. Shown by direct sabotage of internal/api/batch.go.
func TestApiBatch14_MemberOutsideTheSocietyIsUnknown(t *testing.T) {
	w := newBatchWorld(t, 1)
	foreign := newSociety(t).member("")
	before := stateOf(t, w.s.id)
	a := refusal(t, w.post(t, newKey(), file("2025-12-31", []string{w.members[0], foreign}, 10, 20)), http.StatusUnprocessableEntity, "unknown_member")
	b := refusal(t, w.post(t, newKey(), file("2025-12-31", []string{w.members[0], noSuchRow}, 10, 20)), http.StatusUnprocessableEntity, "unknown_member")
	if len(a.Rows) != 1 || a.Rows[0].Line != 3 || len(b.Rows) != 1 || b.Rows[0].Line != 3 {
		t.Fatalf("UNNAMED — want line 3 named in both, got %+v and %+v", a.Rows, b.Rows)
	}
	if a.Message != b.Message {
		t.Fatalf("DISCLOSED — another society's member is refused differently from a nonexistent one: %q vs %q", a.Message, b.Message)
	}
	nothingWritten(t, w.s.id, before, "a row named no member of this society")
}

func TestApiBatch15_NothingToPost(t *testing.T) {
	w := newBatchWorld(t, 0)
	before := stateOf(t, w.s.id)
	refusal(t, w.post(t, newKey(), file("2025-12-31", nil)), http.StatusUnprocessableEntity, "nothing_to_post")
	nothingWritten(t, w.s.id, before, "the batch had no rows")
}

// §0: money is a non-negative decimal kobo string here — a movement's size.
// A ₦0 row and a negative row are refused for DIFFERENT reasons: zero means
// nothing (more likely a parsing artifact than an intention); a negative is
// a member who owes — a loan, not savings — and a migration must not create
// debt from a spreadsheet column.
//
// LIMIT, stated where it will be read: the zero case is also refused by the
// schema (amount_not_zero, 0001), so the break zero_written goes red through
// the schema's 500, not through the handler; the mutation run does not show
// which layer caught it. The test still pins the handler (it wants 422
// zero_amount naming the row), so removing the handler's check turns it red,
// never green. The negative case is the handler's alone: a negative line is a
// legal line. Both shown by direct sabotage of internal/api/batch.go.
func TestApiBatch16_AmountsAreMagnitudeStrings(t *testing.T) {
	w := newBatchWorld(t, 2)
	before := stateOf(t, w.s.id)
	for _, c := range []struct {
		what   string
		amount any
		total  any
		status int
		code   string
	}{
		{"a JSON number", 500000, "500010", http.StatusBadRequest, ""},
		{"a negative amount", "-500000", "500010", http.StatusBadRequest, ""}, // the total its magnitude would give
		{"a fractional amount", "5000.50", "5010.50", http.StatusBadRequest, ""},
		{"a total sent as a JSON number", "500000", 500010, http.StatusBadRequest, ""},
		{"a zero amount", "0", "10", http.StatusUnprocessableEntity, "zero_amount"},
	} {
		body := file("2025-12-31", w.members, 10, 1)
		body.Rows[1].AmountKobo, body.ConfirmedTotalKobo = c.amount, c.total
		r := w.post(t, newKey(), body)
		if r.status != c.status {
			t.Fatalf("ACCEPTED — %s answered %d, want %d: %s", c.what, r.status, c.status, r.body)
		}
		if c.code != "" {
			refusal(t, r, c.status, c.code)
		}
		nothingWritten(t, w.s.id, before, c.what+" was refused")
	}
}

func TestApiBatch17_AsOfInTheFutureIsRefused(t *testing.T) {
	w := newBatchWorld(t, 2)
	var today, tomorrow string
	fixture(t, db.QueryRow(bg(), `SELECT auth_today()::text, (auth_today() + 1)::text`).Scan(&today, &tomorrow), "today")
	before := stateOf(t, w.s.id)
	refusal(t, w.post(t, newKey(), file(tomorrow, w.members[:1], 10)), http.StatusUnprocessableEntity, "as_of_in_future")
	nothingWritten(t, w.s.id, before, "the as-at date was tomorrow")
	record(t, w.post(t, newKey(), file(today, w.members[1:], 10)), http.StatusCreated) // control: today is not the future
}

func TestApiBatch18_AsOfInAClosedPeriodIsRefused(t *testing.T) {
	w := newBatchWorld(t, 1)
	w.s.close(w.s.period("2025-01-01", "2025-12-31"))
	before := stateOf(t, w.s.id)
	refusal(t, w.post(t, newKey(), file("2025-06-30", w.members, 10)), http.StatusUnprocessableEntity, "period_closed")
	nothingWritten(t, w.s.id, before, "the as-at date fell in a closed period")
}

// =============================================================================
// WHO MAY POST
// =============================================================================

// The secretary imports (permissions.ts: import_members). Not the treasurer,
// not the read-only auditor; to anyone without a current post here, the
// society does not exist.
func TestApiBatch19_OnlyTheSecretaryImports(t *testing.T) {
	w := newBatchWorld(t, 1)
	key, body := newKey(), file("2025-12-31", w.members, 10)
	elsewhere := newBatchWorld(t, 0)
	people := []struct {
		who, user string
		status    int
	}{
		{"the treasurer", w.person(t, "treasurer", "2020-01-01", ""), http.StatusForbidden},
		{"the internal auditor", w.person(t, "internal_auditor", "2020-01-01", ""), http.StatusForbidden},
		{"a member with no post", w.person(t, "", "", ""), http.StatusNotFound},
		{"a former secretary", w.person(t, "secretary", "2019-01-01", "2019-12-31"), http.StatusNotFound},
		{"another society's secretary", elsewhere.secretary, http.StatusNotFound},
	}
	before := stateOf(t, w.s.id)
	var notFound []byte
	for _, p := range people {
		// Each person carries a valid PIN proof of their OWN, so the only thing
		// that can refuse them is who they are. Without it, the step-up's 403
		// masks the authorisation's: a handler letting the treasurer import
		// would still answer 403 (step_up_required), and this test passed
		// against exactly that break.
		mine := body
		proof := mintProof(t, p.user)
		mine.PinProofId = &proof
		r, err := postBatch(t, http.DefaultClient, w.srv, w.s.id, key, signIn(t, p.user), mine)
		fixture(t, err, "POST as "+p.who)
		if r.status != p.status {
			t.Fatalf("WRONG ANSWER — %s got %d, want %d: %s", p.who, r.status, p.status, r.body)
		}
		if p.status == http.StatusForbidden && !bytes.Contains(r.body, []byte(`"not_permitted"`)) {
			t.Fatalf("WRONG REASON — %s was refused, but not for their post: %s", p.who, r.body)
		}
		if p.status == http.StatusNotFound {
			if notFound != nil && !bytes.Equal(r.body, notFound) {
				t.Fatalf("DISCLOSED — %s's 404 differs from the others: %s vs %s", p.who, r.body, notFound)
			}
			notFound = r.body
		}
	}
	r, err := postBatch(t, http.DefaultClient, w.srv, noSuchRow, key, signIn(t, w.secretary), body)
	fixture(t, err, "POST to a nonexistent society")
	if r.status != http.StatusNotFound || !bytes.Equal(r.body, notFound) {
		t.Fatalf("DISCLOSED — a society that does not exist answers %d %s; one the caller holds no post in answers %s",
			r.status, r.body, notFound)
	}
	nothingWritten(t, w.s.id, before, "no one entitled had posted")
	record(t, w.post(t, key, body), http.StatusCreated) // control: the same call, by the secretary
}

func TestApiBatch20_NoValidSessionGetsUnauthorized(t *testing.T) {
	w := newBatchWorld(t, 1)
	tok := signIn(t, w.secretary)
	fixture(t, one(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE token_digest = $1`, auth.TokenDigest(tok)), "end session")
	before := stateOf(t, w.s.id)
	for what, token := range map[string]string{"no token": "", "an invented token": "not-a-session", "an ended session": tok} {
		r, err := postBatch(t, http.DefaultClient, w.srv, w.s.id, newKey(), token, file("2025-12-31", w.members, 10))
		fixture(t, err, "POST with "+what)
		if r.status != http.StatusUnauthorized {
			t.Fatalf("ACCEPTED — %s got %d, want 401", what, r.status)
		}
	}
	nothingWritten(t, w.s.id, before, "no valid session")
}

// =============================================================================
// THE SOCIETY'S CHART, AND THE KEY'S SCOPE
// =============================================================================

func TestApiBatch21_ChartThatCannotTakeTheBatchRefuses(t *testing.T) {
	for _, c := range []struct {
		what string
		opt  worldOpt
	}{
		{"no migration_control account", worldOpt{noMigration: true}},
		{"two member_savings accounts", worldOpt{twoSavings: true}},
	} {
		w := newBatchWorld(t, 1, c.opt)
		before := stateOf(t, w.s.id)
		r := w.post(t, newKey(), file("2025-12-31", w.members, 10))
		if r.status != http.StatusConflict {
			t.Fatalf("GUESSED — with %s the batch answered %d, want 409: %s", c.what, r.status, r.body)
		}
		nothingWritten(t, w.s.id, before, c.what)
	}
}

func TestApiBatch22_ReferenceIsPerSociety(t *testing.T) {
	a, b := newBatchWorld(t, 1), newBatchWorld(t, 1)
	key := newKey()
	record(t, a.post(t, key, file("2025-12-31", a.members, 10)), http.StatusCreated)
	if rec := record(t, b.post(t, key, file("2025-12-31", b.members, 10)), http.StatusCreated); rec.AlreadyPosted {
		t.Fatalf("CROSSED — another society's import under the same reference was answered as already posted")
	}
	eachOnce(t, b.members, "NOT POSTED")
}
