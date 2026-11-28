//go:build tier1

// ACCEPTANCE TESTS — PERIOD CLOSE OVER HTTP (0044, stage 4, 2026-11-27). One
// officer proposes closing the books through a date; a DIFFERENT one confirms
// (the trial balance kept and shown) or rejects; every refusal in words; and
// a member's statement says where the books are closed (the reviewer's
// question: what does a member see).
package tier1

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

type pcOut struct {
	CloseId, Through, State string
	Decision                *struct {
		Confirmed    bool
		TrialBalance *[]struct{ Code, BalanceKobo string }
	}
}

func pcFrom(t *testing.T, r resp, status int, what string) pcOut {
	t.Helper()
	var p pcOut
	if r.status != status || json.Unmarshal(r.body, &p) != nil {
		t.Fatalf("REFUSED — %s: %d %s; want %d", what, r.status, r.body, status)
	}
	return p
}

func (w *mapi) proposeClose(t *testing.T, as, through string) resp {
	return w.call(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/period-closes", map[string]string{"through": through, "pinProofId": stepUp(t, as, "period_close")})
}

func (w *mapi) decideClose(t *testing.T, as, id string, confirmed bool, reason string) resp {
	body := map[string]any{"confirmed": confirmed, "pinProofId": stepUp(t, as, "period_close_decision")}
	if reason != "" {
		body["reason"] = reason
	}
	return w.call(t, http.MethodPost, as, "/period-closes/"+id+"/decisions", body)
}

func (w *mapi) closedVia(t *testing.T) *string {
	t.Helper()
	r := w.call(t, http.MethodGet, w.s1, "/societies/"+w.c.s.id+"/period-closes", nil)
	var l struct{ ClosedThrough *string }
	if r.status != http.StatusOK || json.Unmarshal(r.body, &l) != nil {
		t.Fatalf("NOT READ — the closes: %d %s", r.status, r.body)
	}
	return l.ClosedThrough
}

func TestApiCloses01_TwoOfficersCloseAndTheTrialBalanceIsShown(t *testing.T) {
	w := newMoneyAPI(t)
	p := pcFrom(t, w.proposeClose(t, w.s1, pcThrough), http.StatusCreated, "the treasurer's proposal")
	if p.State != "waiting" || p.Through != pcThrough || w.closedVia(t) != nil {
		t.Fatalf("WRONG PROPOSAL — %+v; want waiting through %s, the books not yet closed", p, pcThrough)
	}
	wantProblem(t, w.decideClose(t, w.s1, p.CloseId, true, ""), http.StatusForbidden, "second_officer_required", "the proposer confirming")
	c := pcFrom(t, w.decideClose(t, w.s2, p.CloseId, true, ""), http.StatusCreated, "the chairman confirming")
	if c.State != "confirmed" || c.Decision == nil || c.Decision.TrialBalance == nil || len(*c.Decision.TrialBalance) == 0 {
		t.Fatalf("UNSHOWN — %+v; want confirmed, with the trial balance kept", c)
	}
	var sum int64
	for _, b := range *c.Decision.TrialBalance {
		k, _ := strconv.ParseInt(b.BalanceKobo, 10, 64)
		sum += k
	}
	if sum != 0 {
		t.Fatalf("WRONG TRIAL BALANCE — the kept balances sum to %d", sum)
	}
	if d := w.closedVia(t); d == nil || *d != pcThrough {
		t.Fatalf("UNSAID — the books read closed through %v; want %s", d, pcThrough)
	}
}

func TestApiCloses02_ADayToComeOrBackwardsIsRefusedInWords(t *testing.T) {
	w := newMoneyAPI(t)
	var tomorrow string
	fixture(t, db.QueryRow(bg(), `SELECT (society_today() + 1)::text`).Scan(&tomorrow), "tomorrow")
	wantProblem(t, w.proposeClose(t, w.s1, tomorrow), http.StatusUnprocessableEntity, "not_future", "a close through a day to come")
	p := pcFrom(t, w.proposeClose(t, w.s1, pcThrough), http.StatusCreated, "the proposal")
	pcFrom(t, w.decideClose(t, w.s2, p.CloseId, true, ""), http.StatusCreated, "confirmed")
	wantProblem(t, w.proposeClose(t, w.s1, "2026-09-15"), http.StatusConflict, "not_forward", "a close earlier than the books are closed")
}

func TestApiCloses03_BooksThatDoNotBalanceAreNeverClosed(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.bMem, 100000)
	pcUnbalance(t, w.lastEntry, w.c.bank)
	p := pcFrom(t, w.proposeClose(t, w.s1, pcThrough), http.StatusCreated, "the proposal")
	wantProblem(t, w.decideClose(t, w.s2, p.CloseId, true, ""), http.StatusConflict, "books_do_not_balance", "a close over books that do not sum to zero")
}

// The reviewer's question: what does a member see? Where the books are closed.
func TestApiCloses04_TheMembersStatementSaysWhereTheBooksAreClosed(t *testing.T) {
	w := newMoneyAPI(t)
	read := func() *string {
		r := w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/statement?from=2026-01-01&to=2026-12-31", nil)
		var s struct{ BooksClosedThrough *string }
		if r.status != http.StatusOK || json.Unmarshal(r.body, &s) != nil {
			t.Fatalf("NOT READ — the borrower's statement: %d %s", r.status, r.body)
		}
		return s.BooksClosedThrough
	}
	if d := read(); d != nil {
		t.Fatalf("INVENTED — before any close the statement reads closed through %s", *d)
	}
	p := pcFrom(t, w.proposeClose(t, w.s1, pcThrough), http.StatusCreated, "the proposal")
	pcFrom(t, w.decideClose(t, w.s2, p.CloseId, true, ""), http.StatusCreated, "confirmed")
	if d := read(); d == nil || *d != pcThrough {
		t.Fatalf("UNSAID — the member's statement reads closed through %v; want %s, so the screen can say entries up to then are final", d, pcThrough)
	}
}
