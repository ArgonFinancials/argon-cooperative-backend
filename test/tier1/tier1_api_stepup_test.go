//go:build tier1

// ACCEPTANCE TESTS — the PIN step-up over HTTP: POST /auth/pin-proofs, and
// postAll spending its proof. Written before the handlers, for review.
//
// The user's terms (2026-10-01), each a test below:
//   - the endpoint is GENERAL, and a proof is scoped to the act it is minted
//     for — one act, one proof, the kind bound at mint (U1, U7);
//   - a wrong PIN counts towards the same lockout as sign-in (U2, U3);
//   - a repeat needs no proof: nothing attributable happens (U11);
//   - a refused or failed call does not spend the proof, so a quick retry can
//     reuse it (U12, U13) — the same principle as not claiming the reference;
//   - the proof is not mintable by the code path that uses it (U6 here; and
//     by transaction in TestProof4, by role in the probe's P11–P14).
//
// Which handler runs: default api.NewServer; ARGON_TEST_API_FAKE=<break> the
// fake (apifake_stepup_test.go, and the step-up in apifake_batch_test.go).
package tier1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

const stepPIN = "4827"

// stepWorld: a batch world whose secretary has a real PIN.
func stepWorld(t *testing.T, n int) *bworld {
	t.Helper()
	w := newBatchWorld(t, n)
	fixture(t, one(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, w.secretary, hashPIN(stepPIN)), "the secretary's PIN")
	return w
}

func askProof(t *testing.T, w *bworld, token, pin, act string) resp {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"pin": pin, "act": act})
	req, _ := http.NewRequest(http.MethodPost, w.srv.URL+"/auth/pin-proofs", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, "POST /auth/pin-proofs")
	defer r.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r.Body)
	return resp{r.StatusCode, buf.Bytes()}
}

type pinProofBody struct {
	ProofId   string `json:"proofId"`
	Act       string `json:"act"`
	ExpiresAt string `json:"expiresAt"`
}

func minted(t *testing.T, r resp) string {
	t.Helper()
	if r.status != http.StatusCreated {
		t.Fatalf("REFUSED — the step-up answered %d: %s", r.status, r.body)
	}
	var p pinProofBody
	if err := json.Unmarshal(r.body, &p); err != nil || p.ProofId == "" {
		t.Fatalf("WRONG SHAPE — not a proof: %v: %s", err, r.body)
	}
	return p.ProofId
}

func proofCount(t *testing.T, user string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM auth_proofs WHERE user_id = $1`, user).Scan(&n), "proofs")
	return n
}

func spent(t *testing.T, proof string) bool {
	t.Helper()
	var s bool
	fixture(t, db.QueryRow(bg(), `SELECT consumed_at IS NOT NULL FROM auth_proofs WHERE id = $1`, proof).Scan(&s), "proof state")
	return s
}

// withProof posts body with exactly this proof (nil: none at all).
func (w *bworld) withProof(t *testing.T, key string, body bBody, proof *string) resp {
	t.Helper()
	body.PinProofId = proof
	r, err := postBatch(t, http.DefaultClient, w.srv, w.s.id, key, signIn(t, w.secretary), body)
	fixture(t, err, "POST batch")
	return r
}

func p(s string) *string { return &s }

// =============================================================================
// THE ENDPOINT
// =============================================================================

func TestApiStepUp01_PinProofIsMintedForTheCallerAndTheAct(t *testing.T) {
	w := stepWorld(t, 1)
	id := minted(t, askProof(t, w, signIn(t, w.secretary), stepPIN, "ledger_batch"))
	var user string
	var kind *string
	var used bool
	fixture(t, db.QueryRow(bg(), `SELECT user_id::text, act_kind, consumed_at IS NOT NULL FROM auth_proofs WHERE id = $1`, id).Scan(&user, &kind, &used), "the proof")
	if user != w.secretary || kind == nil || *kind != "ledger_batch" || used {
		t.Fatalf("WRONG PROOF — user %s, act %s, spent %v; want the caller's, for ledger_batch, unspent", user, str(kind), used)
	}
	record(t, w.withProof(t, newKey(), file("2025-12-31", w.members, 10), &id), http.StatusCreated) // and it authorises the import
}

func TestApiStepUp02_WrongPinMintsNothingAndIsCounted(t *testing.T) {
	w := stepWorld(t, 0)
	var before int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1 AND NOT succeeded`, w.secretary).Scan(&before), "attempts")
	r := askProof(t, w, signIn(t, w.secretary), "0000", "ledger_batch")
	if r.status != http.StatusForbidden || !bytes.Contains(r.body, []byte(`"pin_refused"`)) {
		t.Fatalf("WRONG ANSWER — a wrong PIN answered %d %s; want 403 pin_refused", r.status, r.body)
	}
	var after int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1 AND NOT succeeded`, w.secretary).Scan(&after), "attempts")
	if n := proofCount(t, w.secretary); n != 0 || after != before+1 {
		t.Fatalf("WRONG STATE — %d proof(s) minted, failures %d→%d; want none, and one more failure", n, before, after)
	}
}

func TestApiStepUp03_LockedAccountMintsNothing(t *testing.T) {
	w := stepWorld(t, 0)
	tok := signIn(t, w.secretary)
	for i := 0; i < 5; i++ {
		askProof(t, w, tok, "0000", "ledger_batch")
	}
	r := askProof(t, w, tok, stepPIN, "ledger_batch")
	if r.status != http.StatusTooManyRequests || proofCount(t, w.secretary) != 0 {
		t.Fatalf("ORACLE — after five wrong PINs the right one answered %d %s (%d proofs); want 429, nothing minted", r.status, r.body, proofCount(t, w.secretary))
	}
}

func TestApiStepUp04_NoValidSessionMintsNothing(t *testing.T) {
	w := stepWorld(t, 0)
	tok := signIn(t, w.secretary)
	fixture(t, one(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE token_digest = $1`, auth.TokenDigest(tok)), "end session")
	for what, token := range map[string]string{"no token": "", "an ended session": tok} {
		if r := askProof(t, w, token, stepPIN, "ledger_batch"); r.status != http.StatusUnauthorized {
			t.Fatalf("ACCEPTED — %s got %d, want 401", what, r.status)
		}
	}
	if n := proofCount(t, w.secretary); n != 0 {
		t.Fatalf("MINTED — %d proof(s) without a session", n)
	}
}

func TestApiStepUp05_UnknownActMintsNothing(t *testing.T) {
	w := stepWorld(t, 0)
	if r := askProof(t, w, signIn(t, w.secretary), stepPIN, "anything"); r.status != http.StatusBadRequest {
		t.Fatalf("ACCEPTED — a proof for no real act answered %d %s; want 400", r.status, r.body)
	}
	if n := proofCount(t, w.secretary); n != 0 {
		t.Fatalf("MINTED — %d proof(s) for no real act", n)
	}
}

// =============================================================================
// THE BATCH SPENDS A PROOF IT DID NOT MINT
// =============================================================================

func stepRefused(t *testing.T, r resp, what string) {
	t.Helper()
	if r.status != http.StatusForbidden || !bytes.Contains(r.body, []byte(`"step_up_required"`)) {
		t.Fatalf("ACCEPTED — %s answered %d %s; want 403 step_up_required", what, r.status, r.body)
	}
}

// The path that spends a proof must not be able to mint one: with no proof,
// it refuses — and no proof appears.
func TestApiStepUp06_BatchWithoutAProofWritesNothingAndMintsNothing(t *testing.T) {
	w := stepWorld(t, 1)
	key, body := newKey(), file("2025-12-31", w.members, 10)
	before := stateOf(t, w.s.id)
	stepRefused(t, w.withProof(t, key, body, nil), "a batch with no proof")
	nothingWritten(t, w.s.id, before, "no proof was given")
	if n := proofCount(t, w.secretary); n != 0 {
		t.Fatalf("SELF-MINTED — the batch path created %d proof(s) for itself: the step-up is decorative", n)
	}
	record(t, w.withProof(t, key, body, w.proof(t)), http.StatusCreated) // the refusal did not claim the key
}

func TestApiStepUp07_ProofForAnotherActIsRefused(t *testing.T) {
	w := stepWorld(t, 1)
	other := minted(t, askProof(t, w, signIn(t, w.secretary), stepPIN, "pin_change"))
	before := stateOf(t, w.s.id)
	stepRefused(t, w.withProof(t, newKey(), file("2025-12-31", w.members, 10), &other), "a proof minted for a PIN change")
	nothingWritten(t, w.s.id, before, "the proof was for another act")
	if spent(t, other) {
		t.Fatalf("SPENT — a refused import used up the PIN-change proof")
	}
}

func TestApiStepUp08_SpentProofIsRefused(t *testing.T) {
	w := stepWorld(t, 2)
	proof := w.proof(t)
	record(t, w.withProof(t, newKey(), file("2025-12-31", w.members[:1], 10), proof), http.StatusCreated)
	before := stateOf(t, w.s.id)
	stepRefused(t, w.withProof(t, newKey(), file("2025-12-31", w.members[1:], 20), proof), "a second import on one proof")
	nothingWritten(t, w.s.id, before, "the proof was already spent")
}

func TestApiStepUp09_StaleProofIsRefused(t *testing.T) {
	w := stepWorld(t, 1)
	proof := w.proof(t)
	fixture(t, one(`UPDATE auth_proofs SET at = at - interval '6 minutes' WHERE id = $1`, *proof), "age the proof past five minutes")
	before := stateOf(t, w.s.id)
	stepRefused(t, w.withProof(t, newKey(), file("2025-12-31", w.members, 10), proof), "a proof minted six minutes ago")
	nothingWritten(t, w.s.id, before, "the proof was stale")
}

func TestApiStepUp10_AnotherPersonsProofIsRefused(t *testing.T) {
	w := stepWorld(t, 1)
	theirs := mintProof(t, w.person(t, "treasurer", "2020-01-01", ""))
	before := stateOf(t, w.s.id)
	stepRefused(t, w.withProof(t, newKey(), file("2025-12-31", w.members, 10), &theirs), "the treasurer's proof on the secretary's import")
	nothingWritten(t, w.s.id, before, "the proof was someone else's")
}

// Kept exactly as designed: a client retrying a timeout must not have to
// re-enter a PIN to be told that nothing changed.
func TestApiStepUp11_RepeatNeedsNoProof(t *testing.T) {
	w := stepWorld(t, 1)
	key, body := newKey(), file("2025-12-31", w.members, 10)
	record(t, w.withProof(t, key, body, w.proof(t)), http.StatusCreated)
	before := stateOf(t, w.s.id)
	if rec := record(t, w.withProof(t, key, body, nil), http.StatusOK); !rec.AlreadyPosted {
		t.Fatalf("UNTOLD — the repeat did not say it was already posted")
	}
	nothingWritten(t, w.s.id, before, "it was a repeat")
}

// Kept exactly as designed: a correctable mistake does not cost the proof.
func TestApiStepUp12_RefusedBatchKeepsTheProof(t *testing.T) {
	w := stepWorld(t, 2)
	proof := w.proof(t)
	key, body := newKey(), file("2025-12-31", w.members, 10, 20)
	body.ConfirmedTotalKobo = "31"
	refusal(t, w.withProof(t, key, body, proof), http.StatusUnprocessableEntity, "total_mismatch")
	if spent(t, *proof) {
		t.Fatalf("SPENT — a refused import used up the secretary's proof")
	}
	body.ConfirmedTotalKobo = "30"
	record(t, w.withProof(t, key, body, proof), http.StatusCreated)
}

func TestApiStepUp13_FailedBatchKeepsTheProof(t *testing.T) {
	w := stepWorld(t, 3)
	f := inject(t, w.s.id, w.members[2], raiseFault)
	proof := w.proof(t)
	key, body := newKey(), file("2025-12-31", w.members, 10, 20, 30)
	if r := w.withProof(t, key, body, proof); f.count(f.hits) == 0 || r.status != http.StatusInternalServerError {
		t.Fatalf("FIXTURE: the first call was meant to fail at the 3rd row's write; got %d (fault fired %d times)", r.status, f.count(f.hits))
	}
	if spent(t, *proof) {
		t.Fatalf("SPENT — a failed import used up the proof: the act was committed before the write landed")
	}
	f.remove()
	record(t, w.withProof(t, key, body, proof), http.StatusCreated)
}

func TestApiStepUp14_CommittedBatchSpendsItsProofThroughItsOwnAct(t *testing.T) {
	w := stepWorld(t, 1)
	proof := w.proof(t)
	key := newKey()
	record(t, w.withProof(t, key, file("2025-12-31", w.members, 10), proof), http.StatusCreated)
	if !spent(t, *proof) {
		t.Fatalf("UNSPENT — the proof authorised an import and can still be used")
	}
	var kind, actor, used string
	if err := db.QueryRow(bg(), `SELECT a.kind, a.actor_user_id::text, a.proof_id::text FROM ledger_batches b
		JOIN credential_acts a ON a.id = b.authorised_by WHERE b.society_id = $1 AND b.reference = $2`, w.s.id, key).Scan(&kind, &actor, &used); err != nil {
		t.Fatalf("UNLINKED — the batch names no authorising act: %v", err)
	}
	if kind != "ledger_batch" || actor != w.secretary || used != *proof {
		t.Fatalf("WRONG ACT — the batch is authorised by a %s act of %s spending %s; want ledger_batch, the secretary, this proof", kind, actor, used)
	}
}

// The deployed binary runs WITHOUT an auth service until the argon_auth
// connection and the SMS provider exist. The step-up must then say so —
// a deliberate 503 naming the reason — rather than a bare 500 that reads as a
// defect to whoever hits it next. And it mints nothing.
func TestApiStepUp15_UnconfiguredServerSaysSoAndMintsNothing(t *testing.T) {
	w := stepWorld(t, 0)
	var h http.Handler
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		h = newFakeAPIWith(&fakeAPI{db: db, brk: brk, noAuth: true})
	} else {
		var err error
		app, _ := rolePools(t, 0)
		h, err = api.NewServer(api.Deps{DB: app, CSRFKey: testCSRFKey}) // no Auth: as cmd/api is today
		fixture(t, err, "the API without an auth service")
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	w.srv = srv
	r := askProof(t, w, signIn(t, w.secretary), stepPIN, "ledger_batch")
	if r.status != http.StatusServiceUnavailable || !bytes.Contains(r.body, []byte(`"step_up_unavailable"`)) {
		t.Fatalf("UNEXPLAINED — with no auth service the step-up answered %d %s; want 503 step_up_unavailable, saying why", r.status, r.body)
	}
	if n := proofCount(t, w.secretary); n != 0 {
		t.Fatalf("MINTED — %d proof(s) on a server with no auth service", n)
	}
	si := postJSON(t, srv, "/auth/sign-in", "", map[string]string{"credential": "+2348000000000", "pin": stepPIN})
	if si.status != http.StatusServiceUnavailable || !bytes.Contains(si.body, []byte(`"sign_in_unavailable"`)) {
		t.Fatalf("UNEXPLAINED — with no auth service, sign-in answered %d %s; want 503 sign_in_unavailable", si.status, si.body)
	}
}
