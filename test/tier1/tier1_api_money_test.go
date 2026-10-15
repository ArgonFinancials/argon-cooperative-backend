//go:build tier1

// Acceptance tests for the money endpoints (0011, 0012) — written before the
// handlers, for review. Every TestApiMoney* name is here.
//
//	POST /societies/{id}/contributions   record a contribution that arrived
//	POST /approvals/{id}/holds           authorise an approved withdrawal to execute
//	POST /holds/{id}/releases            release a hold whose transfer failed
//	POST /societies/{id}/payouts         record a payout that happened
//
// Proposed (for review):
//   - RECORDING AND AUTHORISING ARE DIFFERENT DUTIES (the role matrix,
//     frontend lib/permissions.ts). Recording money (contributions, payouts) is
//     the treasurer's or the secretary's (record_cash). Authorising a release
//     (the hold, and releasing it) is a signatory's: chairman, treasurer,
//     secretary. An officer here without the duty: 403 not_permitted. Anyone
//     else: 404, identical to a society, request or hold that does not exist.
//   - Every write rests on the caller's own PIN step-up, and the database
//     decides (0011, 0012); the API maps each refusal to what the person can do,
//     carrying the database's reason and figures.
//   - THE SERVER BUILDS THE ENTRY: lines by role, charges from the series (a
//     contribution) or the hold (a payout). A client never sends a charge; one
//     sent is ignored.
//   - THE LEDGER RECORDS WHAT HAPPENED (§11): a contribution the series cannot
//     charge is recorded uncharged, with charges NULL (UNDETERMINED, not zero);
//     a payout that differs from its authorisation is recorded and flagged, and
//     the flags come back in the answer. Refused only when malformed.
//
// Decided 2026-10-14 (0015, LEDGER_CONTRACT §10 widened):
//   - NOBODY AUTHORISES, SIGNS OR ATTESTS TO A MOVEMENT THEY BENEFIT FROM:
//     403 conflict_of_interest. Releasing a hold is one of the four acts: it
//     frees the releaser's own withdrawable.
//   - The officer who authorised a hold never records its payout: 403
//     duties_separated. Authorising is the judgement, recording the attestation.
//   - A contribution carries the client's Idempotency-Key: a retry on a dropped
//     connection answers 200 with the first record and writes nothing.
package tier1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type mapi struct {
	*mworld
	srv       *httptest.Server
	secretary string // the secretary (a signatory, and records money)
	secMem    string
	auditor   string // the internal auditor: an officer, reads, records nothing
	abroad    string // the treasurer of another society
	stranger  string // a member of another society
}

func newMoneyAPI(t *testing.T) *mapi {
	t.Helper()
	w := &mapi{mworld: newMoneyWorld(t), srv: apiServer(t)}
	s := w.c.s
	w.secretary = s.user("Secretary (signatory)")
	w.secMem = s.signatory(w.secretary, "secretary")
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, w.secMem), "active secretary")
	w.auditor = s.user("Internal auditor")
	fixture(t, s.appoint(s.member(w.auditor), "internal_auditor", false, "2020-01-01", ""), "the auditor")
	elsewhere := newSociety(t)
	w.abroad = elsewhere.user("Treasurer elsewhere")
	elsewhere.signatory(w.abroad, "treasurer")
	w.stranger = elsewhere.member(elsewhere.user("Member elsewhere"))
	return w
}

func (w *mapi) call(t *testing.T, method, as, path string, body any) resp {
	t.Helper()
	return w.callH(t, method, as, path, body, nil)
}

func (w *mapi) callH(t *testing.T, method, as, path string, body any, headers map[string]string) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.srv.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+signIn(t, as))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, method+" "+path)
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, b}
}

// ---- the wire, decoded loosely, as a client reads it ---------------------------

type mCharges struct {
	PartnerFeeKobo, VatKobo, StatutoryLevyKobo, ArgonMarginKobo, TotalKobo string
}

type mFlag struct{ Kind, Expected, Observed string }

type mMovement struct {
	EntryId, Kind, MemberId, AmountKobo, MemberAmountKobo, ValueDate string
	HoldId, PayoutReference                                          *string
	ChargeDetermined                                                 bool
	Charges                                                          *mCharges
	Flags                                                            []mFlag
	PostedBy                                                         struct{ UserId, Post, PostLabel string }
}

type mHold struct {
	HoldId, RequestId, MemberId, AmountKobo, TotalKobo, AuthorisedBy, State string
	Charges                                                                 mCharges
	ReleaseReason                                                           *string
}

func decoded[T any](t *testing.T, r resp, want int, what string) T {
	t.Helper()
	if r.status != want {
		t.Fatalf("REFUSED — %s: %d %s; want %d", what, r.status, r.body, want)
	}
	var v T
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("WRONG SHAPE — %s: %v: %s", what, err, r.body)
	}
	return v
}

func k(n int64) string { return strconv.FormatInt(n, 10) }

func wire(c fees) mCharges {
	return mCharges{k(c.fee), k(c.vat), k(c.levy), k(c.margin), k(c.total())}
}

// ---- calls ------------------------------------------------------------------------

func (w *mapi) contribute(t *testing.T, as, member string, kobo any, proof string) resp {
	return w.contributeAt(t, as, w.c.s.id, member, kobo, proof, "")
}

// contributeAt: key "" a fresh Idempotency-Key, "none" no header.
func (w *mapi) contributeAt(t *testing.T, as, society, member string, kobo any, proof, key string) resp {
	h := map[string]string{}
	switch key {
	case "":
		h["Idempotency-Key"] = "CLI-" + digest()[:16]
	case "none":
	default:
		h["Idempotency-Key"] = key
	}
	return w.callH(t, http.MethodPost, as, "/societies/"+society+"/contributions",
		map[string]any{"memberId": member, "amountKobo": kobo, "valueDate": "2026-10-05", "pinProofId": proof}, h)
}

func (w *mapi) authorise(t *testing.T, as, req, proof string) resp {
	return w.call(t, http.MethodPost, as, "/approvals/"+req+"/holds", map[string]any{"pinProofId": proof})
}

func (w *mapi) releaseVia(t *testing.T, as, hold, reason string) resp {
	return w.call(t, http.MethodPost, as, "/holds/"+hold+"/releases", map[string]any{"reason": reason})
}

type payoutCall struct {
	hold, member, ref, proof string
	kobo                     int64
}

func (w *mapi) payOut(t *testing.T, as string, p payoutCall) resp {
	body := map[string]any{"memberId": p.member, "amountKobo": k(p.kobo), "valueDate": "2026-10-14", "payoutReference": p.ref, "pinProofId": p.proof}
	if p.hold != "" {
		body["holdId"] = p.hold
	}
	return w.call(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/payouts", body)
}

// heldVia: an approved request for the borrower and its hold, authorised
// through the API by the CHAIRMAN (fixture), so the treasurer and the
// secretary may record its payout: the authoriser never does (0015).
func (w *mapi) heldVia(t *testing.T, amount int64) (req, hold string) {
	t.Helper()
	req = w.approved(t, w.bMem, amount)
	r := w.authorise(t, w.s2, req, stepUp(t, w.s2, "withdrawal_authorisation"))
	if r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: authorising the payout: %d %s", r.status, r.body)
	}
	var h mHold
	fixture(t, json.Unmarshal(r.body, &h), "the hold")
	return req, h.HoldId
}

func (w *mapi) ref() string { return "NIP-" + digest()[:12] }

func entriesFor(t *testing.T, member string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(DISTINCT entry_id) FROM ledger_lines WHERE member_id = $1`, member).Scan(&n), "the member's entries")
	return n
}

func holdsFor(t *testing.T, req string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM withdrawal_holds WHERE request_id = $1`, req).Scan(&n), "the request's holds")
	return n
}

func savingsOf(t *testing.T, member string) int64 {
	t.Helper()
	var n int64
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(-sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		WHERE a.role = 'member_savings' AND l.member_id = $1`, member).Scan(&n), "the member's savings")
	return n
}

func flagKinds(fs []mFlag) []string {
	var ks []string
	for _, f := range fs {
		ks = append(ks, f.Kind)
	}
	return ks
}

// flagged: `kind` is among the answer's flags, and says what was expected and
// what happened (a mute flag sends the resolver to the logs).
func flagged(t *testing.T, m mMovement, kind, what string) mFlag {
	t.Helper()
	for _, f := range m.Flags {
		if f.Kind == kind {
			if strings.TrimSpace(f.Expected) == "" || strings.TrimSpace(f.Observed) == "" {
				t.Fatalf("MUTE FLAG — %s: %q says expected %q, observed %q", what, kind, f.Expected, f.Observed)
			}
			return f
		}
	}
	t.Fatalf("UNFLAGGED — %s was recorded and the answer's flags are %v; want %q", what, flagKinds(m.Flags), kind)
	return mFlag{}
}

// =============================================================================
// THE STEP-UP: every money act can be stepped up for
// =============================================================================

// Finding 36 (finding 24 recurring): 0011 and 0012 added three acts, and the
// auth service's list did not. The suites never noticed, because their
// fixtures write proofs straight into the database. This asks the API, for
// EVERY act the schema allows a proof for, so the class is caught, not only
// these three: the next migration that adds an act fails here.
func TestApiMoney01_EveryMoneyActCanBeSteppedUpFor(t *testing.T) {
	w := newMoneyAPI(t)
	fixture(t, one(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, w.poster, hashPIN(stepPIN)), "the treasurer's PIN")
	var def string
	fixture(t, db.QueryRow(bg(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'proof_act_kind_known'`).Scan(&def), "the schema's act kinds")
	acts := regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(def, -1)
	if len(acts) < 15 {
		t.Fatalf("FIXTURE: read %d act kinds from %q; the schema has at least 15", len(acts), def)
	}
	for _, a := range acts {
		r := w.call(t, http.MethodPost, w.poster, "/auth/pin-proofs", map[string]string{"pin": stepPIN, "act": a[1]})
		if r.status != http.StatusCreated {
			t.Fatalf("REFUSED — a PIN proof for %s, an act the schema allows: %d %s; without it no client can do this at all", a[1], r.status, r.body)
		}
	}
}

// =============================================================================
// CONTRIBUTIONS
// =============================================================================

func TestApiMoney02_AContributionIsRecordedOnTheRecordersStepUp(t *testing.T) {
	w := newMoneyAPI(t)
	before := savingsOf(t, w.g1Mem)
	m := decoded[mMovement](t, w.contribute(t, w.poster, w.g1Mem, "2500000", stepUp(t, w.poster, "contribution_record")), http.StatusCreated, "a ₦25,000 contribution")
	if m.Kind != "contribution_in" || m.AmountKobo != "2500000" || m.MemberAmountKobo != "2500000" || !m.ChargeDetermined ||
		m.Charges == nil || *m.Charges != wire(fees{}) || m.Flags == nil || len(m.Flags) != 0 {
		t.Fatalf("WRONG RECORD — an uncharged contribution (the society charges none): %+v", m)
	}
	if m.PostedBy.UserId != w.poster || m.PostedBy.Post != "treasurer" {
		t.Fatalf("UNATTRIBUTED — postedBy %+v; want the treasurer, in that post", m.PostedBy)
	}
	if got := savingsOf(t, w.g1Mem); got != before+2500000 {
		t.Fatalf("UNRECORDED — the member's savings are %d; want %d", got, before+2500000)
	}
	var actor string
	fixture(t, db.QueryRow(bg(), `SELECT a.actor_user_id::text FROM ledger_entries e JOIN credential_acts a ON a.id = e.act_id
		WHERE e.id = $1 AND a.kind = 'contribution_record'`, m.EntryId).Scan(&actor), "the entry's act")
	if actor != w.poster {
		t.Fatalf("UNATTRIBUTED — the entry rests on %s's act; want the recorder's own", actor)
	}
}

func TestApiMoney03_AContributionNeedsAStepUp(t *testing.T) {
	w := newMoneyAPI(t)
	n := entriesFor(t, w.g1Mem)
	wantProblem(t, w.contribute(t, w.poster, w.g1Mem, "2500000", noSuchRow), http.StatusForbidden, "step_up_required", "a contribution with a proof that does not exist")
	if entriesFor(t, w.g1Mem) != n {
		t.Fatalf("ACCEPTED — a contribution was recorded without a step-up")
	}
}

// The society's series, computed by the server; a client's figure is ignored.
// WHICH LAYER REFUSES: the API (the server's charge) and, behind it, the DATABASE (entry_charge_is_the_schedules).
// With the API's check removed, the database refuses, the API answers 500, and
// this test is red either way: it catches a wrong result and cannot say which
// layer produced it. A red reading "500" means the database caught what the API
// should have. Its break (suite_money_api.sh) is caught that way.
func TestApiMoney04_TheChargeIsTheServers(t *testing.T) {
	w := newMoneyAPI(t)
	w.c.chargesLike7a(t)
	want, err := quote(w.c.s.id, "contribution_in", 2500000, "2026-10-05")
	fixture(t, err, "the series' charge")
	r := w.callH(t, http.MethodPost, w.poster, "/societies/"+w.c.s.id+"/contributions", map[string]any{"memberId": w.g1Mem, "amountKobo": "2500000",
		"valueDate": "2026-10-05", "pinProofId": stepUp(t, w.poster, "contribution_record"),
		"charges": map[string]string{"partnerFeeKobo": "0", "vatKobo": "0", "statutoryLevyKobo": "0", "argonMarginKobo": "0", "totalKobo": "0"}},
		map[string]string{"Idempotency-Key": "CLI-" + digest()[:16]})
	m := decoded[mMovement](t, r, http.StatusCreated, "a charged contribution")
	if m.Charges == nil || *m.Charges != wire(want) || m.MemberAmountKobo != k(2500000-want.total()) {
		t.Fatalf("WRONG CHARGE — charges %+v, member credited %s; want %+v and %d", m.Charges, m.MemberAmountKobo, wire(want), 2500000-want.total())
	}
}

// Money that arrived is recorded; the charge it could not be given is
// UNDETERMINED, not zero, and the answer says which component is missing.
func TestApiMoney05_AnUndeterminedChargeIsRecordedAndSaysSo(t *testing.T) {
	w := newMoneyAPI(t)
	allSeriesBut(t, w.c.s.id, "contribution_in", "argon_margin")
	r := w.contribute(t, w.poster, w.g1Mem, "2500000", stepUp(t, w.poster, "contribution_record"))
	m := decoded[mMovement](t, r, http.StatusCreated, "a contribution the series cannot charge")
	if m.ChargeDetermined || m.Charges != nil || !strings.Contains(string(r.body), `"charges":null`) || m.MemberAmountKobo != "2500000" {
		t.Fatalf("ZERO IS NOT UNDETERMINED — chargeDetermined %v, charges %+v, member credited %s: %s", m.ChargeDetermined, m.Charges, m.MemberAmountKobo, r.body)
	}
	if f := flagged(t, m, "charge_undetermined", "an uncharged contribution"); !strings.Contains(f.Expected, "argon_margin") {
		t.Fatalf("UNEXPLAINED — the flag does not name the missing component: %+v", f)
	}
}

func TestApiMoney06_RecordingIsTheTreasurersOrTheSecretarys(t *testing.T) {
	w := newMoneyAPI(t)
	m := decoded[mMovement](t, w.contribute(t, w.secretary, w.g1Mem, "2500000", stepUp(t, w.secretary, "contribution_record")), http.StatusCreated, "the secretary recording")
	if m.PostedBy.UserId != w.secretary || m.PostedBy.Post != "secretary" {
		t.Fatalf("UNATTRIBUTED — postedBy %+v; want the secretary, in that post", m.PostedBy)
	}
	n := entriesFor(t, w.g1Mem)
	for who, u := range map[string]string{"the chairman (authorises; does not record)": w.s2, "the internal auditor": w.auditor} {
		wantProblem(t, w.contribute(t, u, w.g1Mem, "2500000", stepUp(t, u, "contribution_record")), http.StatusForbidden, "not_permitted", who+" recording a contribution")
	}
	if entriesFor(t, w.g1Mem) != n {
		t.Fatalf("ACCEPTED — an officer without the duty recorded a contribution")
	}
}

func TestApiMoney07_AnyoneElseGetsNotFound(t *testing.T) {
	w := newMoneyAPI(t)
	none := w.contributeAt(t, w.abroad, noSuchRow, w.g1Mem, "2500000", noSuchRow, "")
	for who, u := range map[string]string{"a member with no post": w.borrower, "the treasurer of another society": w.abroad} {
		r := w.contribute(t, u, w.g1Mem, "2500000", stepUp(t, u, "contribution_record"))
		if r.status != http.StatusNotFound || string(r.body) != string(none.body) {
			t.Fatalf("ACCEPTED — %s: %d %s (no such society: %d %s)", who, r.status, r.body, none.status, none.body)
		}
	}
}

// WHICH LAYER REFUSES: the API (unknown_member) and, behind it, the DATABASE (the line-society rule, 0006).
// With the API's check removed, the database refuses, the API answers 500, and
// this test is red either way: it catches a wrong result and cannot say which
// layer produced it. A red reading "500" means the database caught what the API
// should have. Its break (suite_money_api.sh) is caught that way.
func TestApiMoney08_TheMemberIsThisSocietys(t *testing.T) {
	w := newMoneyAPI(t)
	wantProblem(t, w.contribute(t, w.poster, w.stranger, "2500000", stepUp(t, w.poster, "contribution_record")), http.StatusUnprocessableEntity, "unknown_member",
		"a contribution to another society's member")
	if entriesFor(t, w.stranger) != 0 {
		t.Fatalf("ACCEPTED — another society's member was credited")
	}
}

func TestApiMoney09_MoneyIsAStringOnTheWire(t *testing.T) {
	w := newMoneyAPI(t)
	if r := w.contribute(t, w.poster, w.g1Mem, 2500000, stepUp(t, w.poster, "contribution_record")); r.status != http.StatusBadRequest {
		t.Fatalf("ACCEPTED — amountKobo as a JSON number: %d %s; want 400", r.status, r.body)
	}
	r := w.contribute(t, w.poster, w.g1Mem, "2500000", stepUp(t, w.poster, "contribution_record"))
	if r.status != http.StatusCreated || !strings.Contains(string(r.body), `"amountKobo":"2500000"`) {
		t.Fatalf("ACCEPTED — the answer's amountKobo is not the string \"2500000\": %d %s", r.status, r.body)
	}
}

// WHICH LAYER REFUSES: the API (zero_amount) and, behind it, the DATABASE (a zero line is never written).
// With the API's check removed, the database refuses, the API answers 500, and
// this test is red either way: it catches a wrong result and cannot say which
// layer produced it. A red reading "500" means the database caught what the API
// should have. Its break (suite_money_api.sh) is caught that way.
func TestApiMoney10_AZeroContributionIsRefused(t *testing.T) {
	w := newMoneyAPI(t)
	n := entriesFor(t, w.g1Mem)
	wantProblem(t, w.contribute(t, w.poster, w.g1Mem, "0", stepUp(t, w.poster, "contribution_record")), http.StatusUnprocessableEntity, "zero_amount", "a contribution of nothing")
	if entriesFor(t, w.g1Mem) != n {
		t.Fatalf("ACCEPTED — a contribution of nothing was recorded")
	}
}

// =============================================================================
// THE HOLD: AUTHORISED TO EXECUTE
// =============================================================================

// The charges are fixed when the payout is authorised, by the server; a
// client's figures are ignored.
func TestApiMoney11_AHoldCarriesTheServersCharges(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.approved(t, w.bMem, payKobo)
	r := w.call(t, http.MethodPost, w.poster, "/approvals/"+req+"/holds", map[string]any{"pinProofId": stepUp(t, w.poster, "withdrawal_authorisation"),
		"charges": map[string]string{"partnerFeeKobo": "0", "vatKobo": "0", "statutoryLevyKobo": "0", "argonMarginKobo": "0", "totalKobo": "0"}})
	h := decoded[mHold](t, r, http.StatusCreated, "authorising an approved withdrawal")
	if h.Charges != wire(payFees()) || h.TotalKobo != k(payKobo+payFees().total()) || h.AmountKobo != k(payKobo) ||
		h.MemberId != w.bMem || h.AuthorisedBy != w.poster || h.State != "open" {
		t.Fatalf("WRONG HOLD — %+v; want %d plus the series' %+v, open, by the treasurer", h, payKobo, wire(payFees()))
	}
}

func TestApiMoney12_AHoldNeedsAStepUp(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.approved(t, w.bMem, payKobo)
	wantProblem(t, w.authorise(t, w.poster, req, noSuchRow), http.StatusForbidden, "step_up_required", "authorising with a proof that does not exist")
	if holdsFor(t, req) != 0 {
		t.Fatalf("ACCEPTED — a payout was authorised without a step-up")
	}
}

// The refusal says what the request is: the database's words.
func TestApiMoney13_OnlyAnApprovedWithdrawalIsAuthorised(t *testing.T) {
	w := newMoneyAPI(t)
	req, err := w.raise(w.bMem, payKobo, 2)
	fixture(t, err, "an unsigned request")
	r := w.authorise(t, w.poster, req, stepUp(t, w.poster, "withdrawal_authorisation"))
	wantProblem(t, r, http.StatusConflict, "not_approved", "authorising a request nobody has signed")
	if !strings.Contains(string(r.body), "not an approved withdrawal") {
		t.Fatalf("UNEXPLAINED — the refusal does not carry the database's reason: %s", r.body)
	}
}

func TestApiMoney14_OneOpenHoldPerRequest(t *testing.T) {
	w := newMoneyAPI(t)
	req, _ := w.heldVia(t, payKobo)
	wantProblem(t, w.authorise(t, w.poster, req, stepUp(t, w.poster, "withdrawal_authorisation")), http.StatusConflict, "hold_open", "authorising the same request twice")
	if holdsFor(t, req) != 1 {
		t.Fatalf("ACCEPTED — a second hold on one request")
	}
}

// Beyond what the member may withdraw: refused with the database's figures,
// which only the database can give (savings, guarantees held, floor).
func TestApiMoney15_BeyondWithdrawableIsRefusedWithTheFigures(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.approved(t, w.bMem, borrowerWithdrawable)
	r := w.authorise(t, w.poster, req, stepUp(t, w.poster, "withdrawal_authorisation"))
	wantProblem(t, r, http.StatusUnprocessableEntity, "exceeds_withdrawable", "authorising all the borrower may withdraw, with the charges on top")
	if !strings.Contains(string(r.body), "held for guarantees") || !strings.Contains(string(r.body), k(borrowerWithdrawable)) {
		t.Fatalf("UNEXPLAINED — the refusal does not carry the database's figures: %s", r.body)
	}
	if holdsFor(t, req) != 0 {
		t.Fatalf("ACCEPTED — a hold beyond what the member may withdraw")
	}
}

func TestApiMoney16_AuthorisingIsASignatorysDuty(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.approved(t, w.bMem, payKobo)
	wantProblem(t, w.authorise(t, w.auditor, req, stepUp(t, w.auditor, "withdrawal_authorisation")), http.StatusForbidden, "not_permitted", "the internal auditor authorising a payout")
	if holdsFor(t, req) != 0 {
		t.Fatalf("ACCEPTED — the auditor authorised a payout")
	}
	decoded[mHold](t, w.authorise(t, w.s2, req, stepUp(t, w.s2, "withdrawal_authorisation")), http.StatusCreated, "the chairman (a signatory) authorising")
}

// An unset floor: the society has not decided, and the refusal says so in
// the database's words.
func TestApiMoney17_AnUnsetFloorIsNamed(t *testing.T) {
	w := newMoneyAPI(t)
	fixture(t, one(`UPDATE societies SET account_floor_kobo = NULL WHERE id = $1`, w.c.s.id), "no floor")
	r := w.authorise(t, w.poster, w.approved(t, w.bMem, payKobo), stepUp(t, w.poster, "withdrawal_authorisation"))
	wantProblem(t, r, http.StatusUnprocessableEntity, "not_configured", "authorising where the society has set no floor")
	if !strings.Contains(string(r.body), "has not set its account floor") {
		t.Fatalf("UNEXPLAINED — the refusal does not say what is unset: %s", r.body)
	}
}

// =============================================================================
// RELEASING A HOLD
// =============================================================================

func TestApiMoney18_AReleaseSaysWhy(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	wantProblem(t, w.releaseVia(t, w.poster, hold, " "), http.StatusUnprocessableEntity, "reason_required", "releasing a hold without a reason")
	h := decoded[mHold](t, w.releaseVia(t, w.poster, hold, "NIP returned 'beneficiary account closed'"), http.StatusCreated, "releasing with a reason")
	if h.State != "released" || h.ReleaseReason == nil || *h.ReleaseReason != "NIP returned 'beneficiary account closed'" {
		t.Fatalf("UNRECORDED — the released hold reads %+v", h)
	}
}

func TestApiMoney19_APaidHoldIsNeverReleased(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "the payout")
	wantProblem(t, w.releaseVia(t, w.poster, hold, "changed my mind"), http.StatusConflict, "hold_paid", "releasing a hold that was paid")
}

// =============================================================================
// RECORDING A PAYOUT: WHAT HAPPENED
// =============================================================================

// WHICH LAYER REFUSES: the API (the hold's charges) and, behind it, the DATABASE (entry_charge_is_the_holds).
// With the API's check removed, the database refuses, the API answers 500, and
// this test is red either way: it catches a wrong result and cannot say which
// layer produced it. A red reading "500" means the database caught what the API
// should have. Its break (suite_money_api.sh) is caught that way.
func TestApiMoney20_APayoutOnItsHoldIsRecorded(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	before := savingsOf(t, w.bMem)
	ref := w.ref()
	m := decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: ref, kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "the payout its hold authorised")
	if m.Kind != "withdrawal_out" || m.Charges == nil || *m.Charges != wire(payFees()) || m.MemberAmountKobo != k(payKobo+payFees().total()) ||
		m.HoldId == nil || *m.HoldId != hold || m.PayoutReference == nil || *m.PayoutReference != ref || m.Flags == nil || len(m.Flags) != 0 {
		t.Fatalf("WRONG RECORD — %+v; want the hold's charges on top, the hold and the reference, no flags", m)
	}
	if got := savingsOf(t, w.bMem); got != before-payKobo-payFees().total() {
		t.Fatalf("UNRECORDED — the member's savings are %d; want %d", got, before-payKobo-payFees().total())
	}
}

func TestApiMoney21_APayoutNeedsAStepUp(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	n := entriesFor(t, w.bMem)
	wantProblem(t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: noSuchRow}),
		http.StatusForbidden, "step_up_required", "a payout with a proof that does not exist")
	if entriesFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — a payout was recorded without a step-up")
	}
}

// The bank paid something else: recorded as it happened, and flagged with
// both figures.
func TestApiMoney22_ADifferentAmountIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	m := decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: w.ref(), kobo: 500000, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "a payout of ₦5,000 on a ₦6,000 hold")
	if f := flagged(t, m, "payout_amount_differs", "a different amount"); !strings.Contains(f.Expected, k(payKobo)) || !strings.Contains(f.Observed, "500000") {
		t.Fatalf("UNEXPLAINED — the flag does not carry both figures: %+v", f)
	}
}

func TestApiMoney23_APayoutWithNoHoldIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyAPI(t)
	m := decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "a payout nobody authorised")
	flagged(t, m, "payout_without_hold", "a payout with no hold")
}

// One transfer, one record: the second is refused, naming the first.
func TestApiMoney24_ATransferIsRecordedOnce(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	ref := w.ref()
	first := decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: ref, kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "the payout")
	n := entriesFor(t, w.bMem)
	r := w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: ref, kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")})
	wantProblem(t, r, http.StatusConflict, "already_recorded", "the same transfer recorded again")
	if !strings.Contains(string(r.body), first.EntryId) {
		t.Fatalf("UNEXPLAINED — the refusal does not name the entry that recorded it: %s", r.body)
	}
	if entriesFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — one transfer recorded twice")
	}
}

func TestApiMoney25_AHeldPayoutIsTheHoldsMembers(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	n := entriesFor(t, w.g1Mem)
	wantProblem(t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.g1Mem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusUnprocessableEntity, "not_the_holds_member", "the borrower's hold paid out of a guarantor's savings")
	if entriesFor(t, w.g1Mem) != n {
		t.Fatalf("ACCEPTED — a held payout from another member's savings")
	}
}

func TestApiMoney26_APayoutOnAReleasedHoldIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	decoded[mHold](t, w.releaseVia(t, w.poster, hold, "NIP timed out; the bank said it failed"), http.StatusCreated, "the release")
	m := decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "the payout that went through after all")
	flagged(t, m, "payout_on_released_hold", "a payout on a released hold")
}

func TestApiMoney27_RecordingAPayoutIsTheTreasurersOrTheSecretarys(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	n := entriesFor(t, w.bMem)
	wantProblem(t, w.payOut(t, w.s2, payoutCall{hold: hold, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.s2, "withdrawal_payout")}),
		http.StatusForbidden, "not_permitted", "the chairman recording a payout")
	if entriesFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — the chairman recorded a payout")
	}
	m := decoded[mMovement](t, w.payOut(t, w.secretary, payoutCall{hold: hold, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.secretary, "withdrawal_payout")}),
		http.StatusCreated, "the secretary recording the payout")
	if m.PostedBy.UserId != w.secretary || m.PostedBy.Post != "secretary" {
		t.Fatalf("UNATTRIBUTED — postedBy %+v", m.PostedBy)
	}
}

// WHICH LAYER REFUSES: the API (a blank reference) and, behind it, the DATABASE (withdrawal_names_payout_reference).
// With the API's check removed, the database refuses, the API answers 500, and
// this test is red either way: it catches a wrong result and cannot say which
// layer produced it. A red reading "500" means the database caught what the API
// should have. Its break (suite_money_api.sh) is caught that way.
func TestApiMoney28_APayoutNamesItsTransfer(t *testing.T) {
	w := newMoneyAPI(t)
	_, hold := w.heldVia(t, payKobo)
	n := entriesFor(t, w.bMem)
	r := w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: "", kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")})
	if r.status != http.StatusBadRequest || entriesFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — a payout with no transfer reference: %d %s; want 400 and nothing written", r.status, r.body)
	}
}

// =============================================================================
// 0015: A CONTRIBUTION IS RECORDED ONCE
// =============================================================================

// A dropped connection on 3G: the client retries with the same key. The
// repeat writes nothing, needs no step-up, and answers with the FIRST record.
// Two retries: one with a FRESH proof (a client that steps up again: only the
// key stands between it and a second record), and one resending the request
// as it was, its proof already spent by the first (needs no step-up).
func TestApiMoney29_ARetriedContributionIsRecordedOnce(t *testing.T) {
	w := newMoneyAPI(t)
	key := "CLI-" + digest()[:16]
	proof := stepUp(t, w.poster, "contribution_record")
	first := decoded[mMovement](t, w.contributeAt(t, w.poster, w.c.s.id, w.g1Mem, "2500000", proof, key), http.StatusCreated, "the contribution")
	n := entriesFor(t, w.g1Mem)
	// In this order (a slice, not a map): the fresh-proof retry first, so a
	// key that is not looked up shows as RECORDED TWICE, not as a step-up refusal.
	for _, rt := range []struct{ what, proof string }{
		{"with a fresh step-up", stepUp(t, w.poster, "contribution_record")},
		{"resent as it was (its proof spent)", proof},
	} {
		what := rt.what
		r := w.contributeAt(t, w.poster, w.c.s.id, w.g1Mem, "2500000", rt.proof, key)
		if r.status == http.StatusCreated {
			t.Fatalf("RECORDED TWICE — the same key, retried %s, was recorded again: %s", what, r.body)
		}
		again := decoded[mMovement](t, r, http.StatusOK, "the same contribution retried "+what)
		if again.EntryId != first.EntryId || entriesFor(t, w.g1Mem) != n {
			t.Fatalf("RECORDED TWICE — retried %s, the answer is entry %s (first %s); the member's entries went from %d to %d",
				what, again.EntryId, first.EntryId, n, entriesFor(t, w.g1Mem))
		}
	}
}

func TestApiMoney30_AContributionCarriesItsIdempotencyKey(t *testing.T) {
	w := newMoneyAPI(t)
	n := entriesFor(t, w.g1Mem)
	r := w.contributeAt(t, w.poster, w.c.s.id, w.g1Mem, "2500000", stepUp(t, w.poster, "contribution_record"), "none")
	if r.status != http.StatusBadRequest || entriesFor(t, w.g1Mem) != n {
		t.Fatalf("ACCEPTED — a contribution with no Idempotency-Key: %d %s; want 400 and nothing written", r.status, r.body)
	}
}

// =============================================================================
// 0015: NOBODY AUTHORISES OR ATTESTS TO A MOVEMENT THEY BENEFIT FROM
// =============================================================================

// ownRequest: an approved withdrawal paying the TREASURER, signed by the
// chairman and the secretary, with savings of their own to pay it from.
func (w *mapi) ownRequest(t *testing.T) string {
	t.Helper()
	w.saved(t, w.s1Mem, loanSavings)
	req, err := w.raise(w.s1Mem, payKobo, 2)
	fixture(t, err, "the treasurer's own withdrawal request")
	fixture(t, w.c.s.sign(req, w.s2Mem, "chairman"), "the chairman's signature")
	fixture(t, w.c.s.sign(req, w.secMem, "secretary"), "the secretary's signature")
	return req
}

// WHICH LAYER REFUSES: until 0015's migration, the API's own check (the
// suite's base has only 0015's column); from it, the API and, behind it, the
// DATABASE (contribution_not_recorded_by_beneficiary). With the API's check removed the database refuses and the
// API answers 500: red either way, and a red reading "500" means the
// database caught what the API should have.
func TestApiMoney31_NobodyRecordsTheirOwnContribution(t *testing.T) {
	w := newMoneyAPI(t)
	n := entriesFor(t, w.s1Mem)
	wantProblem(t, w.contribute(t, w.poster, w.s1Mem, "2500000", stepUp(t, w.poster, "contribution_record")), http.StatusForbidden, "conflict_of_interest",
		"the treasurer recording money into their own savings")
	if entriesFor(t, w.s1Mem) != n {
		t.Fatalf("ACCEPTED — the treasurer credited their own savings")
	}
}

// WHICH LAYER REFUSES: until 0015's migration, the API's own check (the
// suite's base has only 0015's column); from it, the API and, behind it, the
// DATABASE (authoriser_not_beneficiary). With the API's check removed the database refuses and the
// API answers 500: red either way, and a red reading "500" means the
// database caught what the API should have.
func TestApiMoney32_NobodyAuthorisesTheirOwnPayout(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.ownRequest(t)
	wantProblem(t, w.authorise(t, w.poster, req, stepUp(t, w.poster, "withdrawal_authorisation")), http.StatusForbidden, "conflict_of_interest",
		"the treasurer authorising their own payout")
	if holdsFor(t, req) != 0 {
		t.Fatalf("ACCEPTED — the treasurer authorised their own payout")
	}
}

// WHICH LAYER REFUSES: until 0015's migration, the API's own check (the
// suite's base has only 0015's column); from it, the API and, behind it, the
// DATABASE (payout_not_recorded_by_beneficiary). With the API's check removed the database refuses and the
// API answers 500: red either way, and a red reading "500" means the
// database caught what the API should have.
func TestApiMoney33_NobodyRecordsTheirOwnPayout(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.ownRequest(t)
	h := decoded[mHold](t, w.authorise(t, w.s2, req, stepUp(t, w.s2, "withdrawal_authorisation")), http.StatusCreated, "the chairman authorising it")
	n := entriesFor(t, w.s1Mem)
	wantProblem(t, w.payOut(t, w.poster, payoutCall{hold: h.HoldId, member: w.s1Mem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusForbidden, "conflict_of_interest", "the treasurer recording their own payout")
	if entriesFor(t, w.s1Mem) != n {
		t.Fatalf("ACCEPTED — the treasurer recorded their own payout")
	}
}

// =============================================================================
// 0015: AUTHORISING AND ATTESTING ARE DIFFERENT DUTIES
// =============================================================================

// WHICH LAYER REFUSES: until 0015's migration, the API's own check (the
// suite's base has only 0015's column); from it, the API and, behind it, the
// DATABASE (authoriser_never_records). With the API's check removed the database refuses and the
// API answers 500: red either way, and a red reading "500" means the
// database caught what the API should have.
func TestApiMoney34_TheAuthoriserNeverRecordsThePayout(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.approved(t, w.bMem, payKobo)
	h := decoded[mHold](t, w.authorise(t, w.secretary, req, stepUp(t, w.secretary, "withdrawal_authorisation")), http.StatusCreated,
		"the secretary (a signatory, who also records money) authorising it")
	n := entriesFor(t, w.bMem)
	wantProblem(t, w.payOut(t, w.secretary, payoutCall{hold: h.HoldId, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.secretary, "withdrawal_payout")}),
		http.StatusForbidden, "duties_separated", "the secretary recording the payout they authorised")
	if entriesFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — one person authorised and recorded the same payout")
	}
	decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{hold: h.HoldId, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "the treasurer recording it instead")
}

// WHICH LAYER REFUSES: until 0015's migration, the API's own check (the
// suite's base has only 0015's column); from it, the API and, behind it, the
// DATABASE (releaser_not_beneficiary). With the API's check removed the database
// refuses and the API answers 500: red either way, and a red reading "500"
// means the database caught what the API should have.
func TestApiMoney35_NobodyReleasesTheirOwnHold(t *testing.T) {
	w := newMoneyAPI(t)
	req := w.ownRequest(t)
	h := decoded[mHold](t, w.authorise(t, w.s2, req, stepUp(t, w.s2, "withdrawal_authorisation")), http.StatusCreated, "the chairman authorising it")
	wantProblem(t, w.releaseVia(t, w.poster, h.HoldId, "the transfer failed"), http.StatusForbidden, "conflict_of_interest",
		"the treasurer releasing the hold on their own payout")
	var released bool
	fixture(t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = $1)`, h.HoldId).Scan(&released), "the release")
	if released {
		t.Fatalf("ACCEPTED — the treasurer freed their own withdrawable")
	}
	decoded[mHold](t, w.releaseVia(t, w.s2, h.HoldId, "the transfer failed"), http.StatusCreated, "the chairman releasing it instead")
}
