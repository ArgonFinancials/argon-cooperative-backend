//go:build tier1

// Suite claims_api — A MEMBER'S CONTRIBUTION, CLAIMED, OVER HTTP (stage 4,
// 2026-11-11). Written before the handlers (internal/api/claims.go). The
// rules the database keeps are suite 0032's; these are what HTTP adds:
// only the member, the session id cleaned before it is checked, a repeat
// answering the first claim, the state as the database has it the moment
// the claim is made, the member's own list, and where to pay.
package tier1

import (
	"encoding/json"
	"net/http"
	"testing"
)

type cClaim struct {
	ClaimId, MemberId, SessionId, AmountKobo, ClaimedAt, State string
	EntryId, CreditedKobo, Detail                              *string
}

type capi struct {
	*mapi
	socAcct string
}

func newClaimAPI(t *testing.T) *capi {
	t.Helper()
	w := &capi{mapi: newMoneyAPI(t), socAcct: vfdNumber()}
	_, err := recordSocietyAccount(w.c.s.id, w.socAcct, opName, opHost, socAcctReason)
	fixture(t, err, "the society's VFD account")
	return w
}

func (w *capi) claimVia(t *testing.T, as, member, session string, amount int64) resp {
	t.Helper()
	return w.call(t, http.MethodPost, as, "/members/"+member+"/contribution-claims", map[string]any{"sessionId": session, "amountKobo": k(amount)})
}

func (w *capi) claimsVia(t *testing.T, as, member string) resp {
	t.Helper()
	return w.call(t, http.MethodGet, as, "/members/"+member+"/contribution-claims", nil)
}

func claimsCount(t *testing.T, member string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM contribution_claims WHERE member_id = $1`, member).Scan(&n), "the claims")
	return n
}

func wantClaimProblem(t *testing.T, r resp, status int, code, what string) {
	t.Helper()
	var p struct{ Code, Message string }
	_ = json.Unmarshal(r.body, &p)
	if r.status != status || p.Code != code || p.Message == "" {
		t.Fatalf("WRONG ANSWER — %s: want %d %s with a message, got %d %s", what, status, code, r.status, r.body)
	}
}

func TestApiClaims01_OnlyTheMemberClaims(t *testing.T) {
	w := newClaimAPI(t)
	none := w.claimVia(t, w.g1, noSuchRow, sessionNew(), 50000)
	for who, u := range map[string]string{"the treasurer": w.poster, "another member": w.g1, "the treasurer of another society": w.abroad} {
		r := w.claimVia(t, u, w.bMem, sessionNew(), 50000)
		if r.status != http.StatusNotFound || string(r.body) != string(none.body) {
			t.Fatalf("ACCEPTED — %s claimed a payment as the borrower's: %d %s", who, r.status, r.body)
		}
	}
	if n := claimsCount(t, w.bMem); n != 0 {
		t.Fatalf("WRITTEN — %d claims made for the borrower by other people", n)
	}
	decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, sessionNew(), 50000), http.StatusCreated, "the member's own claim")
}

func TestApiClaims02_ASessionIsCleanedThenChecked(t *testing.T) {
	w := newClaimAPI(t)
	s := sessionNew()
	spaced := s[:10] + " " + s[10:20] + "  " + s[20:]
	c := decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, spaced, 50000), http.StatusCreated, "a session id typed with spaces")
	if c.SessionId != s {
		t.Fatalf("UNCLEANED — the session id is recorded as %q, not %q", c.SessionId, s)
	}
	wantClaimProblem(t, w.claimVia(t, w.borrower, w.bMem, "12345", 50000), http.StatusUnprocessableEntity, "bad_session", "a session id too short to be one")
	wantClaimProblem(t, w.claimVia(t, w.borrower, w.bMem, sessionNew(), 0), http.StatusUnprocessableEntity, "zero_amount", "a claim of nothing")
}

func TestApiClaims03_ARepeatAnswersTheFirstClaimAndWritesNothing(t *testing.T) {
	w := newClaimAPI(t)
	s := sessionNew()
	first := decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, s, 50000), http.StatusCreated, "the claim")
	again := decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, s, 90000), http.StatusOK, "the same session claimed again, another amount")
	if again.ClaimId != first.ClaimId || again.AmountKobo != "50000" {
		t.Fatalf("REWRITTEN — a repeat answered %+v; the first claim was %+v", again, first)
	}
	if n := claimsCount(t, w.bMem); n != 1 {
		t.Fatalf("WRITTEN — %d claims after a repeat", n)
	}
}

func TestApiClaims04_AClaimOnAPaymentVFDConfirmedIsPostedAtOnce(t *testing.T) {
	w := newClaimAPI(t)
	s := sessionNew()
	_, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 250000)
	fixture(t, err, "VFD's confirmation, before the claim")
	c := decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, s, 250000), http.StatusCreated, "the claim")
	if c.State != "posted" || c.EntryId == nil || c.CreditedKobo == nil || *c.CreditedKobo != "250000" {
		t.Fatalf("STALE — the claim answered %+v; the database credited it in that very transaction", c)
	}
	w2 := decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, sessionNew(), 10000), http.StatusCreated, "a claim VFD has not confirmed")
	if w2.State != "waiting" || w2.EntryId != nil || w2.CreditedKobo != nil {
		t.Fatalf("WRONG — a claim with no payment behind it answered %+v", w2)
	}
}

func TestApiClaims05_TheListIsTheMembersOwnNewestFirst(t *testing.T) {
	w := newClaimAPI(t)
	a := decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, sessionNew(), 10000), http.StatusCreated, "the first claim")
	b := decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, sessionNew(), 20000), http.StatusCreated, "the second claim")
	decoded[cClaim](t, w.claimVia(t, w.g1, w.g1Mem, sessionNew(), 30000), http.StatusCreated, "another member's claim")
	l := decoded[struct{ Claims []cClaim }](t, w.claimsVia(t, w.borrower, w.bMem), http.StatusOK, "the member's claims")
	if len(l.Claims) != 2 || l.Claims[0].ClaimId != b.ClaimId || l.Claims[1].ClaimId != a.ClaimId {
		t.Fatalf("WRONG LIST — want the member's two claims, newest first: %+v", l.Claims)
	}
	if r := w.claimsVia(t, w.g1, w.bMem); r.status != http.StatusNotFound {
		t.Fatalf("ACCEPTED — another member read the borrower's claims: %d %s", r.status, r.body)
	}
}

func TestApiClaims06_WhereToPayIsTheSocietysAccountReadByTheMember(t *testing.T) {
	w := newClaimAPI(t)
	var acct struct {
		AccountNumber, BankName string
		AccountName             *string
	}
	acct = decoded[struct {
		AccountNumber, BankName string
		AccountName             *string
	}](t, w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/contribution-account", nil), http.StatusOK, "where to pay")
	if acct.AccountNumber != w.socAcct || acct.BankName != "VFD Microfinance Bank" {
		t.Fatalf("WRONG ACCOUNT — %+v; the society's VFD account is %s", acct, w.socAcct)
	}
	if r := w.call(t, http.MethodGet, w.g1, "/members/"+w.bMem+"/contribution-account", nil); r.status != http.StatusNotFound {
		t.Fatalf("ACCEPTED — another member read the borrower's payment account: %d %s", r.status, r.body)
	}
	// A society whose account is not recorded yet says so.
	bare := newMoneyAPI(t)
	wantClaimProblem(t, bare.call(t, http.MethodGet, bare.borrower, "/members/"+bare.bMem+"/contribution-account", nil),
		http.StatusUnprocessableEntity, "no_account", "a society with no VFD account")
}
