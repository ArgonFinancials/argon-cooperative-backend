//go:build tier1

// Suite resolutions_api — THE OFFICERS' PAYMENT ENDPOINTS OVER HTTP (stage 4,
// 2026-11-11): the exceptions, the unmatched payments with their claims, and
// a resolution proposed and decided by two officers. The rules are suite
// 0033's; these are what HTTP adds: who reads and who acts, the list's
// contents, the key, the open proposal named, every refusal saying which,
// the posted contribution answered, the state filter.
package tier1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

type rRes struct {
	ResolutionId, Decision, Reason, State, ProposedAt string
	InflowId, ClaimId, MemberId, AmountKobo, EntryId  *string
}

type rapi struct{ *capi }

func newResolveAPI(t *testing.T) *rapi { t.Helper(); return &rapi{capi: newClaimAPI(t)} }

func (w *rapi) proposeVia(t *testing.T, as, key string, body map[string]any) resp {
	t.Helper()
	body["pinProofId"] = stepUp(t, as, "payment_resolution")
	if key == "" {
		key = fmt.Sprintf("RES-API-%d", seq.Add(1))
	}
	return w.callH(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/payment-resolutions", body, map[string]string{"Idempotency-Key": key})
}

func (w *rapi) confirmVia(t *testing.T, as, id string) resp {
	t.Helper()
	return w.call(t, http.MethodPost, as, "/payment-resolutions/"+id+"/confirmations", map[string]any{"pinProofId": stepUp(t, as, "payment_resolution")})
}

func (w *rapi) rejectVia(t *testing.T, as, id, reason string) resp {
	t.Helper()
	return w.call(t, http.MethodPost, as, "/payment-resolutions/"+id+"/rejections", map[string]any{"reason": reason, "pinProofId": stepUp(t, as, "payment_resolution")})
}

func (w *rapi) payment(t *testing.T, kobo int64) (inflow, session string) {
	t.Helper()
	session = sessionNew()
	in, err := inflowFrom(w.c.s.id, w.socAcct, session, "", kobo)
	fixture(t, err, "a payment nobody matched")
	return in, session
}

const apiWhy = "The sender is the member's employer, by October's payroll schedule"

func problemOf(r resp) (code string, resolution *string) {
	var p struct {
		Code         string
		ResolutionId *string
	}
	_ = json.Unmarshal(r.body, &p)
	return p.Code, p.ResolutionId
}

func TestApiResolve01_OfficersReadTheAuditorReadsOnlyOfficersAct(t *testing.T) {
	w := newResolveAPI(t)
	in, _ := w.payment(t, 50000)
	base := "/societies/" + w.c.s.id
	for _, path := range []string{"/wallet-exceptions", "/unmatched-payments", "/payment-resolutions"} {
		if r := w.call(t, http.MethodGet, w.borrower, base+path, nil); r.status != http.StatusNotFound {
			t.Fatalf("ACCEPTED — a member with no post read %s: %d %s", path, r.status, r.body)
		}
		if r := w.call(t, http.MethodGet, w.auditor, base+path, nil); r.status != http.StatusOK {
			t.Fatalf("REFUSED — the internal auditor, who reads everything, was refused %s: %d %s", path, r.status, r.body)
		}
	}
	r := w.proposeVia(t, w.auditor, "", map[string]any{"decision": "credit", "inflowId": in, "memberId": w.bMem, "reason": apiWhy})
	if code, _ := problemOf(r); r.status != http.StatusForbidden || code != "not_permitted" {
		t.Fatalf("ACCEPTED — the internal auditor proposed a credit: %d %s", r.status, r.body)
	}
}

func TestApiResolve02_AnUnmatchedPaymentComesWithEveryClaimOnIt(t *testing.T) {
	w := newResolveAPI(t)
	// Both claims first: a payment arriving after two claims is credited to neither.
	s := sessionNew()
	decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, s, 60000), http.StatusCreated, "one member's claim")
	decoded[cClaim](t, w.claimVia(t, w.g1, w.g1Mem, s, 60000), http.StatusCreated, "another member's claim on it")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 60000)
	fixture(t, err, "the payment, contested")
	credited, cs := w.payment(t, 20000)
	decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, cs, 20000), http.StatusCreated, "a matched claim (credited)")
	l := decoded[struct {
		Payments []struct {
			InflowId string
			Claims   []struct{ MemberId, State string }
		}
	}](t, w.call(t, http.MethodGet, w.poster, "/societies/"+w.c.s.id+"/unmatched-payments", nil), http.StatusOK, "the unmatched payments")
	found := false
	for _, p := range l.Payments {
		if p.InflowId == credited {
			t.Fatalf("LISTED — a payment already credited is listed as unmatched")
		}
		if p.InflowId == in {
			found = true
			if len(p.Claims) != 2 {
				t.Fatalf("HIDDEN — the contested payment is listed with %d claims; two members claim it", len(p.Claims))
			}
		}
	}
	if !found {
		t.Fatalf("UNLISTED — the contested payment is not among the unmatched: %+v", l.Payments)
	}
}

func TestApiResolve03_ASecondOfficerConfirmsAndTheContributionIsAnswered(t *testing.T) {
	w := newResolveAPI(t)
	in, _ := w.payment(t, 70000)
	p := decoded[rRes](t, w.proposeVia(t, w.s1, "", map[string]any{"decision": "credit", "inflowId": in, "memberId": w.bMem, "reason": apiWhy}), http.StatusCreated, "the treasurer proposes")
	if p.State != "open" || p.AmountKobo == nil || *p.AmountKobo != "70000" {
		t.Fatalf("WRONG — the proposal answered %+v; want open, at VFD's ₦700", p)
	}
	if r := w.confirmVia(t, w.s1, p.ResolutionId); r.status != http.StatusForbidden {
		t.Fatalf("ACCEPTED — the proposer confirmed their own proposal: %d %s", r.status, r.body)
	}
	c := decoded[rRes](t, w.confirmVia(t, w.s2, p.ResolutionId), http.StatusOK, "the chairman confirms")
	if c.State != "confirmed" || c.EntryId == nil {
		t.Fatalf("UNTOLD — the confirmation answered %+v; the contribution it posted is not named", c)
	}
	if _, n := postingOf(t, in); n != 1 {
		t.Fatalf("NOT POSTED — %d contributions from the payment two officers credited", n)
	}
}

func TestApiResolve04_ARepeatedKeyAnswersTheFirstProposal(t *testing.T) {
	w := newResolveAPI(t)
	in, _ := w.payment(t, 30000)
	key := fmt.Sprintf("RES-REPEAT-%d", seq.Add(1))
	first := decoded[rRes](t, w.proposeVia(t, w.s1, key, map[string]any{"decision": "credit", "inflowId": in, "memberId": w.bMem, "reason": apiWhy}), http.StatusCreated, "the proposal")
	again := decoded[rRes](t, w.proposeVia(t, w.s1, key, map[string]any{"decision": "credit", "inflowId": in, "memberId": w.g1Mem, "reason": apiWhy}), http.StatusOK, "the same key again")
	if again.ResolutionId != first.ResolutionId {
		t.Fatalf("WRITTEN — a repeat answered %s; the first proposal is %s", again.ResolutionId, first.ResolutionId)
	}
}

func TestApiResolve05_AnOpenProposalIsNamed(t *testing.T) {
	w := newResolveAPI(t)
	in, _ := w.payment(t, 30000)
	first := decoded[rRes](t, w.proposeVia(t, w.s1, "", map[string]any{"decision": "credit", "inflowId": in, "memberId": w.bMem, "reason": apiWhy}), http.StatusCreated, "the proposal")
	r := w.proposeVia(t, w.s2, "", map[string]any{"decision": "credit", "inflowId": in, "memberId": w.g1Mem, "reason": apiWhy})
	code, open := problemOf(r)
	if r.status != http.StatusConflict || code != "open_resolution" || open == nil || *open != first.ResolutionId {
		t.Fatalf("UNNAMED — a second proposal on the payment answered %d %s; want 409 open_resolution naming %s", r.status, r.body, first.ResolutionId)
	}
}

func TestApiResolve06_EveryRefusalSaysWhich(t *testing.T) {
	w := newResolveAPI(t)
	done, s := w.payment(t, 40000)
	decoded[cClaim](t, w.claimVia(t, w.borrower, w.bMem, s, 40000), http.StatusCreated, "the claim that credits it")
	if r := w.proposeVia(t, w.s1, "", map[string]any{"decision": "credit", "inflowId": done, "memberId": w.g1Mem, "reason": apiWhy}); r.status != http.StatusConflict {
		t.Fatalf("WRONG — crediting a payment already credited answered %d %s; want 409 already_credited", r.status, r.body)
	} else if code, _ := problemOf(r); code != "already_credited" {
		t.Fatalf("WRONG — %s, not already_credited", code)
	}
	in, _ := w.payment(t, 40000)
	p := decoded[rRes](t, w.proposeVia(t, w.s1, "", map[string]any{"decision": "credit", "inflowId": in, "memberId": w.bMem, "reason": apiWhy}), http.StatusCreated, "a proposal")
	decoded[rRes](t, w.rejectVia(t, w.s2, p.ResolutionId, "Not this member: the payroll schedule says otherwise"), http.StatusOK, "rejected")
	if r := w.confirmVia(t, w.s2, p.ResolutionId); r.status != http.StatusConflict {
		t.Fatalf("WRONG — confirming a rejected proposal answered %d %s; want 409 not_open", r.status, r.body)
	} else if code, _ := problemOf(r); code != "not_open" {
		t.Fatalf("WRONG — %s, not not_open", code)
	}
	if r := w.proposeVia(t, w.s1, "", map[string]any{"decision": "credit", "inflowId": in, "memberId": w.bMem, "reason": "short"}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("WRONG — a five-character reason answered %d %s; want 422 bad_reason", r.status, r.body)
	}
}

func TestApiResolve07_TheListFiltersByState(t *testing.T) {
	w := newResolveAPI(t)
	a, _ := w.payment(t, 11000)
	b, _ := w.payment(t, 12000)
	pa := decoded[rRes](t, w.proposeVia(t, w.s1, "", map[string]any{"decision": "credit", "inflowId": a, "memberId": w.bMem, "reason": apiWhy}), http.StatusCreated, "one proposal")
	decoded[rRes](t, w.proposeVia(t, w.s1, "", map[string]any{"decision": "credit", "inflowId": b, "memberId": w.bMem, "reason": apiWhy}), http.StatusCreated, "another")
	decoded[rRes](t, w.confirmVia(t, w.s2, pa.ResolutionId), http.StatusOK, "the first confirmed")
	l := decoded[struct{ Resolutions []rRes }](t, w.call(t, http.MethodGet, w.poster, "/societies/"+w.c.s.id+"/payment-resolutions?state=open", nil), http.StatusOK, "the open ones")
	if len(l.Resolutions) != 1 || l.Resolutions[0].State != "open" {
		t.Fatalf("UNFILTERED — ?state=open answered %d resolutions: %+v", len(l.Resolutions), l.Resolutions)
	}
}
