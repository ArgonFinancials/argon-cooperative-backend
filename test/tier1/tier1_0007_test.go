//go:build tier1

// Suite 0007 — THE LOAN BOOK'S RULES (LOAN_SHAPE.md, approved 2026-10-05).
// Written before the migration, for review.
//
// The boundary: the LOAN holds what was agreed (product terms snapshotted,
// principal, term, a fixed schedule, guarantors, the approval); the LEDGER
// holds what moved. Every disbursement and repayment entry names its loan.
// There is no outstanding-balance column to test, because there is none: what
// is owed is derived from the ledger (the reads come with the API package).
//
// Decided at review: a guarantor consents only through their OWN step-up act;
// a signatory on a loan never guarantees it, in either order; the rate is
// product configuration, refused above zero until a method is agreed.
//
// Approval of a disbursement is DERIVED from the signatures (a quorum signed
// before the request expires), never read from approval_requests.state: the
// API role can write that column and nothing ties it to signatures (finding
// 14, open).
package tier1

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func refusedAs(constraint string) want { return want{codes: []string{"23514"}, constraint: constraint} }

// uniqueOrCheck: a rule a unique index may enforce (23505) as well as a trigger (23514).
func uniqueOrCheck(constraint string) want {
	return want{codes: []string{"23505", "23514"}, constraint: constraint}
}

// ---- the fixture -------------------------------------------------------------
//
// Society S: newChart's accounts (bank 1001, savings 2001, member loan 1201).
// Product "ordinary": up to 12 months, 2 guarantors, 3x savings, rate 0,
// prepayment allowed. The borrower has ₦10,000 savings, so may borrow up to
// ₦30,000. Two guarantors and two signatories (treasurer, chairman), all
// active members; the request needs 2 signatures.

type lworld struct {
	c              *chart
	product        string
	borrower, bMem string // user, member
	g1, g1Mem      string
	g2, g2Mem      string
	s1, s1Mem      string // treasurer (signatory)
	s2, s2Mem      string // chairman (signatory)
}

const (
	loanSavings   = int64(1000000) // ₦10,000
	loanPrincipal = int64(2400000) // ₦24,000, within 3x
	loanTerm      = 12
)

func newLoanWorld(t *testing.T) *lworld {
	t.Helper()
	w := &lworld{c: newChart(t)}
	s := w.c.s
	w.borrower, w.bMem = w.person(t, "Borrower")
	w.g1, w.g1Mem = w.person(t, "Guarantor one")
	w.g2, w.g2Mem = w.person(t, "Guarantor two")
	w.s1 = s.user("Treasurer (signatory)")
	w.s1Mem = s.signatory(w.s1, "treasurer")
	w.s2 = s.user("Chairman (signatory)")
	w.s2Mem = s.signatory(w.s2, "chairman")
	for _, m := range []string{w.s1Mem, w.s2Mem} {
		fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, m), "active signatory")
	}
	fixture(t, db.QueryRow(bg(), `INSERT INTO loan_products (society_id, code, name, max_term_months, guarantors_required,
		savings_multiple_pct, interest_rate_bp, allows_prepayment) VALUES ($1, 'ordinary', 'Ordinary loan', 12, 2, 300, 0, true)
		RETURNING id`, s.id).Scan(&w.product), "product")
	_, err := s.postEntry(entry{kind: "contribution_in", valueDate: "2026-09-01"},
		line{acct: w.c.bank, kobo: loanSavings}, line{acct: w.c.savings, kobo: -loanSavings, member: w.bMem})
	fixture(t, err, "the borrower's savings")
	return w
}

// person: a user with an ACTIVE member record in S.
func (w *lworld) person(t *testing.T, name string) (user, member string) {
	t.Helper()
	user = w.c.s.user(name)
	member = w.c.s.member(user)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, member), "active member")
	return user, member
}

type inst struct {
	due  string
	kobo int64
}

// schedule: `term` monthly instalments from 5 November 2026, the last
// carrying the remainder, summing to principal.
func schedule(principal int64, term int) []inst {
	out := []inst{}
	each := principal / int64(term)
	d := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	for i := 0; i < term; i++ {
		amt := each
		if i == term-1 {
			amt = principal - each*int64(term-1)
		}
		out = append(out, inst{d.AddDate(0, i, 0).Format("2006-01-02"), amt})
	}
	return out
}

type loanTerms struct {
	member, product string
	principal       int64
	term            int
	guarantors      int // snapshot; -1 = the product's
	sched           []inst
}

// agree writes a loan and its schedule in one transaction (an agreement).
func (w *lworld) agree(lt loanTerms) (string, error) {
	if lt.member == "" {
		lt.member = w.bMem
	}
	if lt.product == "" {
		lt.product = w.product
	}
	if lt.principal == 0 {
		lt.principal = loanPrincipal
	}
	if lt.term == 0 {
		lt.term = loanTerm
	}
	if lt.guarantors == 0 {
		lt.guarantors = 2
	}
	if lt.sched == nil {
		lt.sched = schedule(lt.principal, lt.term)
	}
	var id string
	err := tx(func(x pgx.Tx) error {
		if err := x.QueryRow(bg(), `INSERT INTO loans (society_id, member_id, product_id, principal_kobo, term_months, purpose,
			guarantors_required, savings_multiple_pct, interest_rate_bp, allows_prepayment)
			VALUES ($1, $2, $3, $4, $5, 'School fees', $6, 300, 0, true) RETURNING id`,
			w.c.s.id, lt.member, lt.product, lt.principal, lt.term, lt.guarantors).Scan(&id); err != nil {
			return err
		}
		for i, in := range lt.sched {
			if _, err := x.Exec(bg(), `INSERT INTO loan_instalments (loan_id, n, due_on, amount_kobo) VALUES ($1, $2, $3, $4)`,
				id, i+1, in.due, in.kobo); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func (w *lworld) loan(t *testing.T) string {
	t.Helper()
	id, err := w.agree(loanTerms{})
	fixture(t, err, "an agreed loan")
	return id
}

// stepUp: the person's own PIN proof for one act kind, minted BEFORE the act's
// transaction (0005: never minted where it is spent).
func stepUp(t *testing.T, user, kind string) string {
	t.Helper()
	var id string
	fixture(t, db.QueryRow(bg(), `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', $2) RETURNING id`,
		user, kind).Scan(&id), "a PIN proof for "+kind)
	return id
}

// guarantee: `actor` takes a step-up and, in one transaction, records the
// act and the guarantee for (guarantorUser, guarantorMember). An honest
// guarantee has actor == guarantorUser.
func (w *lworld) guaranteeAs(t *testing.T, actor, actKind, loan, gUser, gMember string) (string, error) {
	proof := stepUp(t, actor, actKind)
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
			actKind, actor, proof).Scan(&act); err != nil {
			return fmt.Errorf("FIXTURE: the act: %w", err)
		}
		return x.QueryRow(bg(), `INSERT INTO loan_guarantees (loan_id, guarantor_member_id, guarantor_user_id, act_id)
			VALUES ($1, $2, $3, $4) RETURNING id`, loan, gMember, gUser, act).Scan(&id)
	})
	return id, err
}

func (w *lworld) guarantee(t *testing.T, loan, user, member string) (string, error) {
	return w.guaranteeAs(t, user, "guarantee", loan, user, member)
}

func (w *lworld) guaranteed(t *testing.T, loan, user, member string) string {
	t.Helper()
	id, err := w.guarantee(t, loan, user, member)
	fixture(t, err, "a guarantee")
	return id
}

func (w *lworld) withdrawAs(t *testing.T, actor, guarantee string) error {
	proof := stepUp(t, actor, "guarantee_withdrawal")
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('guarantee_withdrawal', $1, $2) RETURNING id`,
			actor, proof).Scan(&act); err != nil {
			return fmt.Errorf("FIXTURE: the act: %w", err)
		}
		_, err := x.Exec(bg(), `INSERT INTO loan_guarantee_withdrawals (guarantee_id, act_id) VALUES ($1, $2)`, guarantee, act)
		return err
	})
}

// request raises the disbursement request for a loan.
func (w *lworld) requestFor(loan, kind, member string, amount int64, expires string) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at, loan_id)
		VALUES ($1, $2, $3, $4, $5, 2, `+expires+`, $6) RETURNING id`,
		w.c.s.id, fmt.Sprintf("LN-%d", seq.Add(1)), member, kind, amount, loan).Scan(&id)
	return id, err
}

func (w *lworld) request(loan string) (string, error) {
	return w.requestFor(loan, "loan_disbursement", w.bMem, loanPrincipal, `now() + interval '7 days'`)
}

// ready: an agreed loan with both guarantees, its request raised and signed
// by both signatories. Everything a disbursement needs.
func (w *lworld) ready(t *testing.T) (loan, req string) {
	t.Helper()
	loan = w.loan(t)
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	req, err := w.request(loan)
	fixture(t, err, "the disbursement request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "first signature")
	fixture(t, w.c.s.sign(req, w.s2Mem, "chairman"), "second signature")
	return loan, req
}

// move posts a loan movement: one entry naming `loan`, with its lines.
func (w *lworld) move(kind, loan, society string, ls ...line) (string, error) {
	if society == "" {
		society = w.c.s.id
	}
	var id string
	err := tx(func(x pgx.Tx) error {
		if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, loan_id)
			VALUES ($1, $2, '2026-10-05', $3, 'treasurer', 'Treasurer', $4) RETURNING id`,
			society, kind, w.c.s.actor, nz(loan)).Scan(&id); err != nil {
			return err
		}
		return w.c.s.insertLines(x, id, ls...)
	})
	return id, err
}

func (w *lworld) disburse(loan string, kobo int64) (string, error) {
	return w.move("loan_disbursement", loan, "", line{acct: w.c.loan, kobo: kobo, member: w.bMem}, line{acct: w.c.bank, kobo: -kobo})
}

func (w *lworld) repay(loan string, kobo int64) (string, error) {
	return w.move("loan_repayment", loan, "", line{acct: w.c.bank, kobo: kobo}, line{acct: w.c.loan, kobo: -kobo, member: w.bMem})
}

// =============================================================================
// THE AGREEMENT: terms are the product's, snapshotted; the record never changes
// =============================================================================

// A rate is configuration, but a rate with no agreed method is invented policy.
// One basis point: the smallest rate there is, so no "small rates are fine"
// threshold can let it through.
func TestLoan1_ProductRateIsZeroUntilAMethodIsAgreed(t *testing.T) {
	s := newChart(t).s
	refused(t, one(`INSERT INTO loan_products (society_id, code, name, max_term_months, guarantors_required, savings_multiple_pct,
		interest_rate_bp, allows_prepayment) VALUES ($1, 'rated', 'Rated', 12, 2, 300, 1, true)`, s.id),
		"a product carrying even one basis point of interest before any society has agreed how interest is applied", refusedAs("interest_method_undecided"))
}

func TestLoan2_LoanCarriesItsProductsTerms(t *testing.T) {
	w := newLoanWorld(t)
	_, err := w.agree(loanTerms{guarantors: 1})
	refused(t, err, "a loan snapshotting 1 guarantor when its product requires 2", refusedAs("loan_terms_are_products"))
}

func TestLoan3_TermWithinTheProductsMaximum(t *testing.T) {
	w := newLoanWorld(t)
	_, err := w.agree(loanTerms{term: 24})
	refused(t, err, "a 24-month loan on a product of at most 12", refusedAs("loan_term_within_product"))
}

func TestLoan4_PrincipalWithinTheSavingsMultiple(t *testing.T) {
	w := newLoanWorld(t)
	_, err := w.agree(loanTerms{principal: 3*loanSavings + 1})
	refused(t, err, "₦30,000.01 on ₦10,000 of savings, against a 3x product", refusedAs("loan_within_savings_multiple"))
}

func TestLoan5_AgreementIsImmutable(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	refused(t, one(`UPDATE loans SET principal_kobo = principal_kobo + 100000 WHERE id = $1`, loan),
		"raising an agreed loan's principal", refusedAs("loan_agreement_immutable"))
	refused(t, one(`DELETE FROM loans WHERE id = $1`, loan), "deleting an agreed loan", refusedAs("loan_agreement_immutable"))
}

func TestLoan6_ScheduleSumsToThePrincipal(t *testing.T) {
	w := newLoanWorld(t)
	sch := schedule(loanPrincipal, loanTerm)
	sch[0].kobo--
	_, err := w.agree(loanTerms{sched: sch})
	refused(t, err, "a schedule one kobo short of the principal", refusedAs("loan_schedule_sums_to_principal"))
}

func TestLoan7_ScheduleHasOneInstalmentPerMonthOfTerm(t *testing.T) {
	w := newLoanWorld(t)
	sch := schedule(loanPrincipal, loanTerm-1) // 11 instalments, still summing to the principal
	_, err := w.agree(loanTerms{sched: sch})
	refused(t, err, "a 12-month loan with 11 instalments", refusedAs("loan_schedule_matches_term"))
}

func TestLoan8_ScheduleIsImmutable(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	refused(t, one(`UPDATE loan_instalments SET due_on = due_on + 30 WHERE loan_id = $1 AND n = 1`, loan),
		"moving an agreed instalment's due date", refusedAs("loan_schedule_immutable"))
	refused(t, one(`DELETE FROM loan_instalments WHERE loan_id = $1 AND n = 12`, loan),
		"deleting an agreed instalment", refusedAs("loan_schedule_immutable"))
}

func TestLoan9_ScheduleCannotGrowAfterAgreement(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	refused(t, one(`INSERT INTO loan_instalments (loan_id, n, due_on, amount_kobo) VALUES ($1, 13, '2027-11-05', 100000)`, loan),
		"adding a thirteenth instalment to an agreed schedule", refusedAs("loan_schedule_closed"))
}

func TestLoan10_ScheduleDueDatesIncrease(t *testing.T) {
	w := newLoanWorld(t)
	sch := schedule(loanPrincipal, loanTerm)
	sch[0].due, sch[1].due = sch[1].due, sch[0].due
	_, err := w.agree(loanTerms{sched: sch})
	refused(t, err, "instalment 1 falling due after instalment 2", refusedAs("loan_schedule_in_order"))
}

func TestLoan11_BorrowerIsAMemberOfTheLoansSociety(t *testing.T) {
	w := newLoanWorld(t)
	elsewhere := newChart(t).s
	_, err := w.agree(loanTerms{member: elsewhere.member(elsewhere.user("Elsewhere"))})
	refused(t, err, "a loan in S to a member of another society", refusedAs("loan_borrower_in_society"))
}

func TestLoan12_ProductIsTheLoansSocietys(t *testing.T) {
	w := newLoanWorld(t)
	other := newLoanWorld(t)
	_, err := w.agree(loanTerms{product: other.product})
	refused(t, err, "a loan in S on another society's product", refusedAs("loan_product_in_society"))
}

// =============================================================================
// GUARANTORS: a personal pledge, by the guarantor's own step-up
// =============================================================================

// An officer cannot consent for a member: consent by proxy is theatre.
func TestLoan13_GuaranteeRestsOnTheGuarantorsOwnAct(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	_, err := w.guaranteeAs(t, w.s1, "guarantee", loan, w.g1, w.g1Mem)
	refused(t, err, "the treasurer's step-up recorded as guarantor one's consent", refusedAs("guarantee_is_own_act"))
}

func TestLoan14_GuaranteeRestsOnAGuaranteeAct(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	_, err := w.guaranteeAs(t, w.g1, "ledger_batch", loan, w.g1, w.g1Mem)
	refused(t, err, "guarantor one's own act, but one taken to authorise an import", refusedAs("guarantee_act_is_guarantee"))
}

// Per PERSON, not per member record (the lesson of signatory_approvals).
func TestLoan15_BorrowerNeverGuaranteesTheirOwnLoan(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	second := w.c.s.member(w.borrower)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, second), "the borrower's second member record")
	_, err := w.guarantee(t, loan, w.borrower, second)
	refused(t, err, "the borrower guaranteeing their own loan through a second member record", refusedAs("guarantor_not_borrower"))
}

func TestLoan16_OneGuaranteePerPerson(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	second := w.c.s.member(w.g1)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, second), "guarantor one's second member record")
	_, err := w.guarantee(t, loan, w.g1, second)
	refused(t, err, "guarantor one counted twice through two member records", uniqueOrCheck("loan_guarantees_one_per_person"))
}

func TestLoan17_GuarantorIsAnActiveMemberOfTheBorrowersSociety(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	elsewhere := newChart(t).s
	u := elsewhere.user("Member elsewhere")
	m := elsewhere.member(u)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, m), "active, in another society")
	_, err := w.guarantee(t, loan, u, m)
	refused(t, err, "a member of another society guaranteeing a loan in S", refusedAs("guarantor_member_of_society"))
	fixture(t, one(`UPDATE members SET status = 'suspended' WHERE id = $1`, w.g2Mem), "a suspended member")
	_, err = w.guarantee(t, loan, w.g2, w.g2Mem)
	refused(t, err, "a suspended member guaranteeing", refusedAs("guarantor_member_of_society"))
}

func TestLoan18_WithdrawalIsTheGuarantorsOwnAct(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	g := w.guaranteed(t, loan, w.g1, w.g1Mem)
	refused(t, w.withdrawAs(t, w.s1, g), "the treasurer withdrawing guarantor one's consent", refusedAs("guarantee_withdrawal_is_own_act"))
}

func TestLoan19_NoWithdrawalAfterDisbursement(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	var g string
	fixture(t, db.QueryRow(bg(), `SELECT id FROM loan_guarantees WHERE loan_id = $1 AND guarantor_user_id = $2`, loan, w.g1).Scan(&g), "guarantee")
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	refused(t, w.withdrawAs(t, w.g1, g), "a guarantor withdrawing after the money has gone out", refusedAs("guarantee_withdrawn_before_disbursement"))
}

// =============================================================================
// APPROVAL: guarantors first; a signatory never guarantees, in either order
// =============================================================================

func TestLoan20_RequestNeedsTheProductsGuarantors(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	_, err := w.request(loan)
	refused(t, err, "raising the disbursement request with 1 of 2 guarantors", refusedAs("loan_guarantors_met"))
	g := w.guaranteed(t, loan, w.g2, w.g2Mem)
	fixture(t, w.withdrawAs(t, w.g2, g), "guarantor two withdraws")
	_, err = w.request(loan)
	refused(t, err, "raising it with 2 guarantees, one of them withdrawn", refusedAs("loan_guarantors_met"))
}

func TestLoan21_GuarantorCannotSignTheLoan(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	w.guaranteed(t, loan, w.s1, w.s1Mem) // the treasurer guarantees
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	req, err := w.request(loan)
	fixture(t, err, "the request")
	refused(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the treasurer signing a loan they guarantee", refusedAs("signatory_not_guarantor"))
}

func TestLoan22_SignatoryCannotThenGuarantee(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	req, err := w.request(loan)
	fixture(t, err, "the request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the treasurer signs")
	_, err = w.guarantee(t, loan, w.s1, w.s1Mem)
	refused(t, err, "the treasurer guaranteeing a loan they signed", refusedAs("signatory_not_guarantor"))
}

func TestLoan23_RequestIsForThisLoan(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	week := `now() + interval '7 days'`
	_, err := w.requestFor(loan, "loan_disbursement", w.bMem, loanPrincipal+1, week)
	refused(t, err, "a request for a kobo more than the principal", refusedAs("loan_request_matches_loan"))
	_, err = w.requestFor(loan, "loan_disbursement", w.g1Mem, loanPrincipal, week)
	refused(t, err, "a request naming a member other than the borrower", refusedAs("loan_request_matches_loan"))
	_, err = w.requestFor(loan, "withdrawal_out", w.bMem, loanPrincipal, week)
	refused(t, err, "a withdrawal request naming a loan", refusedAs("loan_request_matches_loan"))
}

// =============================================================================
// THE LEDGER BOUNDARY: every loan movement names its loan, and obeys it
// =============================================================================

func TestLoan24_LoanMovementNamesItsLoan(t *testing.T) {
	w := newLoanWorld(t)
	_, err := w.disburse("", loanPrincipal)
	refused(t, err, "a loan disbursement naming no loan", refusedAs("loan_movement_names_loan"))
}

func TestLoan25_OnlyLoanMovementsNameALoan(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	_, err := w.move("contribution_in", loan, "", line{acct: w.c.bank, kobo: 5000}, line{acct: w.c.savings, kobo: -5000, member: w.bMem})
	refused(t, err, "a contribution naming a loan", refusedAs("loan_id_only_on_loan_movements"))
}

// A quorum of signatures, each made before the request expired. Never the
// request's state column (finding 14).
func TestLoan26_DisbursementNeedsAQuorumSignedBeforeExpiry(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	req, err := w.request(loan)
	fixture(t, err, "the request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "one signature")
	fixture(t, one(`UPDATE approval_requests SET state = 'approved' WHERE id = $1`, req), "the state column says approved")
	_, err = w.disburse(loan, loanPrincipal)
	refused(t, err, "disbursing on 1 of 2 signatures, with the state column set to approved", refusedAs("loan_disbursement_approved"))

	w2 := newLoanWorld(t)
	loan2 := w2.loan(t)
	w2.guaranteed(t, loan2, w2.g1, w2.g1Mem)
	w2.guaranteed(t, loan2, w2.g2, w2.g2Mem)
	stale, err := w2.requestFor(loan2, "loan_disbursement", w2.bMem, loanPrincipal, `now() - interval '1 day'`)
	fixture(t, err, "a request that expired yesterday")
	fixture(t, w2.c.s.sign(stale, w2.s1Mem, "treasurer"), "a signature after expiry (finding 15: signing does not check)")
	fixture(t, w2.c.s.sign(stale, w2.s2Mem, "chairman"), "a second one")
	_, err = w2.disburse(loan2, loanPrincipal)
	refused(t, err, "disbursing on two signatures made after the request expired", refusedAs("loan_disbursement_approved"))
}

func TestLoan27_DisbursedOnce(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	_, err = w.disburse(loan, loanPrincipal)
	refused(t, err, "disbursing the same loan twice", uniqueOrCheck("loan_disbursed_once"))
}

func TestLoan28_DisbursementIsThePrincipal(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal-1)
	refused(t, err, "disbursing a kobo less than the principal", refusedAs("loan_disbursement_is_principal"))
}

func TestLoan29_DisbursementIsToTheBorrower(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	_, err := w.move("loan_disbursement", loan, "", line{acct: w.c.loan, kobo: loanPrincipal, member: w.g1Mem}, line{acct: w.c.bank, kobo: -loanPrincipal})
	refused(t, err, "a disbursement booked to a guarantor's loan account", refusedAs("loan_line_is_borrowers"))
}

func TestLoan30_NoRepaymentBeforeDisbursement(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	_, err := w.repay(loan, 200000)
	refused(t, err, "a repayment on a loan never disbursed", refusedAs("loan_repaid_after_disbursement"))
}

func TestLoan31_RepaymentIsTheBorrowersLoan(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	_, err = w.move("loan_repayment", loan, "", line{acct: w.c.bank, kobo: 200000}, line{acct: w.c.loan, kobo: -200000, member: w.g1Mem})
	refused(t, err, "a repayment of this loan credited to a guarantor's loan account", refusedAs("loan_line_is_borrowers"))
}

// 0006's lesson at this boundary: a loan of S moved by an entry of T.
func TestLoan32_LoanMovementIsInTheLoansSociety(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	t2 := newChart(t)
	_, err := w.move("loan_disbursement", loan, t2.s.id, line{acct: t2.loan, kobo: loanPrincipal, member: t2.member}, line{acct: t2.bank, kobo: -loanPrincipal})
	refused(t, err, "society T's entry disbursing society S's loan", refusedAs("loan_same_society"))
}

// =============================================================================
// CONTROLS: what must still commit
// =============================================================================

// The whole story. An overpayment is money that arrived: recorded, not refused.
func TestLoan_Control_FullLifecycle(t *testing.T) {
	w := newLoanWorld(t)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	accepted(t, err, "disbursing the principal to the borrower on a quorum, guarantors in place")
	_, err = w.repay(loan, 200000)
	accepted(t, err, "the first instalment repaid")
	_, err = w.repay(loan, loanPrincipal) // more than is now owed
	accepted(t, err, "an overpayment (it surfaces as a disagreement, it is never refused)")
}

// Terms are snapshotted: changing the product changes no agreed loan.
func TestLoan_Control_ProductChangeLeavesAgreedLoansAlone(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	accepted(t, one(`UPDATE loan_products SET guarantors_required = 3 WHERE id = $1`, w.product), "the society raises the product to 3 guarantors")
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	_, err := w.request(loan)
	accepted(t, err, "the loan agreed on 2 guarantors raises its request with 2")
}

func TestLoan_Control_WithdrawnGuarantorReplaced(t *testing.T) {
	w := newLoanWorld(t)
	loan := w.loan(t)
	g := w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	accepted(t, w.withdrawAs(t, w.g1, g), "guarantor one withdraws, before disbursement")
	g3, g3Mem := w.person(t, "Guarantor three")
	_, err := w.guarantee(t, loan, g3, g3Mem)
	accepted(t, err, "a third member guarantees in their place")
	_, err = w.request(loan)
	accepted(t, err, "the request, with two current guarantees")
}
