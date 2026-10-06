//go:build tier1

// Suite 0009 — WRITE-OFFS (LOAN_SHAPE.md §6a, approved 2026-10-06). Written
// before the migration, for review.
//
// A write-off does not make a debt disappear: it records where the loss went,
// who decided, and why. The loan stays as agreed. So:
//   - the quorum is the SOCIETY'S setting (its bye-laws), with no default; an
//     unset quorum refuses IN WORDS that say so;
//   - the quorum is COPIED onto the request when raised and never re-read (the
//     point-in-time rule of the officer's post at posting);
//   - any disbursed loan with money owed may be written off, schedule ended or
//     not; a reason in words is required;
//   - neither the borrower nor a guarantor ever signs (decided); the borrower
//     never signs ANY request for their own loan, the disbursement included
//     (finding 19: 0008 barred guarantors from signing, never the borrower);
//   - the entry is exactly the outstanding, equal to what was approved, once,
//     to a loss account; afterwards no repayment, and money recovered is its
//     own movement that never reopens the loan.
package tier1

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// ---- the fixture -----------------------------------------------------------------

type wworld struct {
	*lworld
	loss, recovery string // accounts
	s3, s3Mem      string // secretary (a third signatory)
}

func newWriteOffs(t *testing.T) *wworld {
	t.Helper()
	w := &wworld{lworld: newLoanWorld(t)}
	s := w.c.s
	for _, a := range []struct {
		dst                    *string
		code, name, kind, role string
	}{{&w.loss, "5901", "Loan losses", "expense", "loan_loss"}, {&w.recovery, "4901", "Loan recoveries", "income", "loan_recovery_income"}} {
		fixture(t, db.QueryRow(bg(), `INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			s.id, a.code, a.name, a.kind, a.role).Scan(a.dst), "account "+a.role)
	}
	w.s3 = s.user("Secretary (signatory)")
	w.s3Mem = s.signatory(w.s3, "secretary")
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, w.s3Mem), "active")
	return w
}

func (w *wworld) quorum(t *testing.T, n any) {
	t.Helper()
	fixture(t, one(`UPDATE societies SET write_off_signatures_required = $1 WHERE id = $2`, n, w.c.s.id), "the society's write-off quorum")
}

// out: a ₦3,000, three-month loan to `member`, guaranteed by g1 and g2 (or the
// given guarantors), its disbursement signed by `signers`, disbursed on
// 5 October 2026.
func (w *wworld) out(t *testing.T, member string, guarantors [][2]string, signers [][2]string) string {
	t.Helper()
	loan, err := w.agree(loanTerms{member: member, principal: lrPrincipal, term: 3})
	fixture(t, err, "an agreed loan")
	if guarantors == nil {
		guarantors = [][2]string{{w.g1, w.g1Mem}, {w.g2, w.g2Mem}}
	}
	for _, g := range guarantors {
		w.guaranteed(t, loan, g[0], g[1])
	}
	req, err := w.requestFor(loan, "loan_disbursement", member, lrPrincipal, `now() + interval '7 days'`)
	fixture(t, err, "the disbursement request")
	if signers == nil {
		signers = [][2]string{{w.s1Mem, "treasurer"}, {w.s2Mem, "chairman"}}
	}
	for _, sg := range signers {
		fixture(t, w.c.s.sign(req, sg[0], sg[1]), "a disbursement signature")
	}
	_, err = w.moveOn("2026-10-05", "loan_disbursement", loan, "", line{acct: w.c.loan, kobo: lrPrincipal, member: member}, line{acct: w.c.bank, kobo: -lrPrincipal})
	fixture(t, err, "the disbursement")
	return loan
}

func (w *wworld) repayOn(t *testing.T, loan, member, on string, kobo int64) {
	t.Helper()
	_, err := w.moveOn(on, "loan_repayment", loan, "", line{acct: w.c.bank, kobo: kobo}, line{acct: w.c.loan, kobo: -kobo, member: member})
	fixture(t, err, "a repayment")
}

func (w *wworld) raise(loan, member string, required int, amount int64, reason any) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at, loan_id, reason)
		VALUES ($1, $2, $3, 'loan_write_off', $4, $5, now() + interval '7 days', $6, $7) RETURNING id`,
		w.c.s.id, "WO-"+digest()[:12], member, amount, required, loan, reason).Scan(&id)
	return id, err
}

const woReason = "The borrower died in August; the estate has no assets"

// approved: a write-off request for `amount`, signed by s1 and s3 (quorum 2).
func (w *wworld) approved(t *testing.T, loan string, amount int64) string {
	t.Helper()
	w.quorum(t, 2)
	req, err := w.raise(loan, w.bMem, 2, amount, woReason)
	fixture(t, err, "the write-off request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "first write-off signature")
	fixture(t, w.c.s.sign(req, w.s3Mem, "secretary"), "second write-off signature")
	return req
}

func (w *wworld) writeOff(loan, member, on string, kobo int64, lossAcct string) (string, error) {
	if lossAcct == "" {
		lossAcct = w.loss
	}
	return w.moveOn(on, "loan_write_off", loan, "", line{acct: lossAcct, kobo: kobo}, line{acct: w.c.loan, kobo: -kobo, member: member})
}

// =============================================================================
// THE QUORUM: the society's, copied at raise time
// =============================================================================

// The refusal must SAY what is undecided: a bare refusal sends someone hunting a bug.
func TestWriteOff1_AnUnsetQuorumRefusesAndSaysSo(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	_, err := w.raise(loan, w.bMem, 2, lrPrincipal, woReason)
	refused(t, err, "a write-off in a society that has set no write-off quorum", refusedAs("write_off_quorum_unset"))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && !strings.Contains(pe.Message, "has not decided how many signatures a write-off needs") {
		t.Fatalf("WRONG REFUSAL — the refusal does not say what is undecided: %q", pe.Message)
	}
}

func TestWriteOff2_TheRequestCarriesTheSocietysQuorum(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.quorum(t, 3)
	_, err := w.raise(loan, w.bMem, 2, lrPrincipal, woReason)
	refused(t, err, "a write-off request asking for 2 signatures when the bye-laws say 3", refusedAs("write_off_quorum_is_societys"))
}

// =============================================================================
// WHAT MAY BE WRITTEN OFF, AND WHY
// =============================================================================

func TestWriteOff3_OnlyADisbursedLoanWithMoneyOwed(t *testing.T) {
	w := newWriteOffs(t)
	w.quorum(t, 2)
	undisbursed, err := w.agree(loanTerms{principal: lrPrincipal, term: 3})
	fixture(t, err, "an agreed loan, never disbursed")
	_, err = w.raise(undisbursed, w.bMem, 2, lrPrincipal, woReason)
	refused(t, err, "writing off a loan never disbursed", refusedAs("write_off_needs_money_owed"))
	repaid := w.out(t, w.bMem, nil, nil)
	w.repayOn(t, repaid, w.bMem, "2026-11-05", lrPrincipal)
	_, err = w.raise(repaid, w.bMem, 2, lrPrincipal, woReason)
	refused(t, err, "writing off a loan already repaid", refusedAs("write_off_needs_money_owed"))
}

func TestWriteOff4_TheRequestIsForWhatIsOwed(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.repayOn(t, loan, w.bMem, "2026-11-05", 100000)
	w.quorum(t, 2)
	_, err := w.raise(loan, w.bMem, 2, lrPrincipal, woReason)
	refused(t, err, "a write-off request for the principal when ₦2,000 is owed", refusedAs("write_off_request_is_outstanding"))
}

func TestWriteOff5_AWriteOffGivesItsReason(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.quorum(t, 2)
	_, err := w.raise(loan, w.bMem, 2, lrPrincipal, nil)
	refused(t, err, "a write-off request with no reason", refusedAs("write_off_states_reason"))
	_, err = w.raise(loan, w.bMem, 2, lrPrincipal, "   ")
	refused(t, err, "a write-off request whose reason is spaces", refusedAs("write_off_states_reason"))
}

// =============================================================================
// WHO NEVER SIGNS
// =============================================================================

// Finding 19: the borrower never signs ANY request for their own loan. Here
// the chairman borrows: they may not sign their own disbursement, nor its
// write-off.
func TestWriteOff6_TheBorrowerNeverSignsARequestForTheirOwnLoan(t *testing.T) {
	w := newWriteOffs(t)
	_, err := w.c.s.postEntry(entry{kind: "contribution_in", valueDate: "2026-09-01"},
		line{acct: w.c.bank, kobo: loanSavings}, line{acct: w.c.savings, kobo: -loanSavings, member: w.s2Mem})
	fixture(t, err, "the chairman's savings")
	loan, err := w.agree(loanTerms{member: w.s2Mem, principal: lrPrincipal, term: 3})
	fixture(t, err, "the chairman's loan")
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	req, err := w.requestFor(loan, "loan_disbursement", w.s2Mem, lrPrincipal, `now() + interval '7 days'`)
	fixture(t, err, "the disbursement request")
	refused(t, w.c.s.sign(req, w.s2Mem, "chairman"), "the chairman signing the disbursement of their own loan",
		refusedAs("borrower_never_signs_own_loan"))

	other := w.out(t, w.s2Mem, nil, [][2]string{{w.s1Mem, "treasurer"}, {w.s3Mem, "secretary"}})
	w.quorum(t, 2)
	wo, err := w.raise(other, w.s2Mem, 2, lrPrincipal, woReason)
	fixture(t, err, "the write-off request")
	refused(t, w.c.s.sign(wo, w.s2Mem, "chairman"), "the chairman signing the write-off of their own loan",
		refusedAs("borrower_never_signs_own_loan"))
}

// Decided: a guarantor never signs whether the debt survives. 0008's rule
// already covers every request naming the loan; this guards against it ever
// being narrowed to disbursements (M0 narrows it, to prove the test can fail).
// The refusal comes from 0008's signatory_not_guarantor, by design: one rule
// for every request naming the loan, not a second copy for write-offs.
func TestWriteOff7_AGuarantorNeverSignsTheWriteOff(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, [][2]string{{w.g1, w.g1Mem}, {w.s3, w.s3Mem}}, nil)
	w.quorum(t, 2)
	wo, err := w.raise(loan, w.bMem, 2, lrPrincipal, woReason)
	fixture(t, err, "the write-off request")
	refused(t, w.c.s.sign(wo, w.s3Mem, "secretary"), "the secretary signing the write-off of a loan they guarantee",
		refusedAs("signatory_not_guarantor"))
}

// =============================================================================
// THE ENTRY: approved, exactly what is owed, once, to a loss account
// =============================================================================

func TestWriteOff8_TheEntryNeedsAnApprovedWriteOff(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.quorum(t, 2)
	req, err := w.raise(loan, w.bMem, 2, lrPrincipal, woReason)
	fixture(t, err, "the write-off request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "one signature of two")
	_, err = w.writeOff(loan, w.bMem, "2026-12-31", lrPrincipal, "")
	refused(t, err, "writing off on one signature of two", refusedAs("write_off_approved"))
}

func TestWriteOff9_TheEntryIsExactlyWhatIsOwed(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.approved(t, loan, lrPrincipal)
	_, err := w.writeOff(loan, w.bMem, "2026-12-31", 100000, "")
	refused(t, err, "writing off ₦1,000 of ₦3,000 owed (a partial loss is a decision this shape does not make)",
		refusedAs("write_off_is_outstanding"))
}

// What was approved is what is written off. A repayment after the approval
// changes what is owed; the approval no longer describes it.
func TestWriteOff10_TheEntryIsWhatWasApproved(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.approved(t, loan, lrPrincipal)
	w.repayOn(t, loan, w.bMem, "2026-11-05", 50000)
	_, err := w.writeOff(loan, w.bMem, "2026-12-31", 250000, "")
	refused(t, err, "writing off the ₦2,500 now owed on an approval for ₦3,000", refusedAs("write_off_is_outstanding"))
}

// NOTE (the P11 pattern): once 0009 is real, the exact-amount rule would ALSO
// refuse a second write-off (nothing is owed by then), at commit. The test
// demands write_off_once by name, which a correct rule raises first (at the
// insert); its mutant lacks both rules, so this goes red on the missing one.
// If the once-rule is ever moved to commit time, re-check this test.
func TestWriteOff11_WrittenOffOnce(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.approved(t, loan, lrPrincipal)
	_, err := w.writeOff(loan, w.bMem, "2026-12-31", lrPrincipal, "")
	fixture(t, err, "the write-off")
	_, err = w.writeOff(loan, w.bMem, "2026-12-31", lrPrincipal, "")
	refused(t, err, "writing the same loan off twice", uniqueOrCheck("write_off_once"))
}

func TestWriteOff12_TheLossGoesToALossAccount(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.approved(t, loan, lrPrincipal)
	_, err := w.writeOff(loan, w.bMem, "2026-12-31", lrPrincipal, w.c.bank)
	refused(t, err, "a write-off debited to the bank", refusedAs("write_off_to_loss_account"))
}

// =============================================================================
// AFTER: no repayment; a recovery is its own movement and never reopens the loan
// =============================================================================

func TestWriteOff13_NoRepaymentAfterAWriteOff(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.approved(t, loan, lrPrincipal)
	_, err := w.writeOff(loan, w.bMem, "2026-12-31", lrPrincipal, "")
	fixture(t, err, "the write-off")
	_, err = w.moveOn("2027-01-10", "loan_repayment", loan, "", line{acct: w.c.bank, kobo: 50000}, line{acct: w.c.loan, kobo: -50000, member: w.bMem})
	refused(t, err, "a repayment to a written-off loan", refusedAs("loan_written_off"))
}

func TestWriteOff14_ARecoveryNeedsAWrittenOffLoan(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	_, err := w.moveOn("2026-12-31", "loan_recovery", loan, "", line{acct: w.c.bank, kobo: 50000}, line{acct: w.recovery, kobo: -50000})
	refused(t, err, "a 'recovery' on a loan never written off", refusedAs("recovery_needs_write_off"))
}

func TestWriteOff15_ARecoveryNeverReopensTheLoan(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.approved(t, loan, lrPrincipal)
	_, err := w.writeOff(loan, w.bMem, "2026-12-31", lrPrincipal, "")
	fixture(t, err, "the write-off")
	_, err = w.moveOn("2027-01-10", "loan_recovery", loan, "", line{acct: w.c.bank, kobo: 50000}, line{acct: w.c.loan, kobo: -50000, member: w.bMem})
	refused(t, err, "a recovery credited to the written-off loan (it would read as an overpayment)", refusedAs("recovery_never_reopens_loan"))
}

func TestWriteOff16_WriteOffsAndRecoveriesNameTheirLoan(t *testing.T) {
	w := newWriteOffs(t)
	_, err := w.writeOff("", w.bMem, "2026-12-31", 1000, "")
	refused(t, err, "a write-off naming no loan", refusedAs("loan_movement_names_loan"))
	_, err = w.moveOn("2026-12-31", "loan_recovery", "", "", line{acct: w.c.bank, kobo: 1000}, line{acct: w.recovery, kobo: -1000})
	refused(t, err, "a recovery naming no loan", refusedAs("loan_movement_names_loan"))
}

// =============================================================================
// CONTROLS
// =============================================================================

// Decided: any disbursed loan with money owed, schedule ended or not. Here it
// is in arrears, mid-schedule. Then money turns up and is recovered.
func TestWriteOff_Control_AnInArrearsLoanWrittenOffThenRecovered(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	w.repayOn(t, loan, w.bMem, "2026-11-05", 100000)
	w.quorum(t, 2)
	req, err := w.raise(loan, w.bMem, 2, 200000, woReason)
	accepted(t, err, "raising a write-off of the ₦2,000 owed on a loan mid-schedule")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "first write-off signature")
	fixture(t, w.c.s.sign(req, w.s3Mem, "secretary"), "second write-off signature")
	_, err = w.writeOff(loan, w.bMem, "2026-12-31", 200000, "")
	accepted(t, err, "writing off the ₦2,000 owed on a loan mid-schedule, on an approved request")
	_, err = w.moveOn("2027-01-10", "loan_recovery", loan, "", line{acct: w.c.bank, kobo: 50000}, line{acct: w.recovery, kobo: -50000})
	accepted(t, err, "₦500 recovered later, to recovery income")
}

// The quorum is the one copied at raise time: a later change to the bye-laws
// does not re-judge a decision already taken.
func TestWriteOff_Control_ALaterQuorumDoesNotRejudge(t *testing.T) {
	w := newWriteOffs(t)
	loan := w.out(t, w.bMem, nil, nil)
	req := w.approved(t, loan, lrPrincipal)
	w.quorum(t, 3)
	_, err := w.writeOff(loan, w.bMem, "2026-12-31", lrPrincipal, "")
	accepted(t, err, "the write-off approved on 2 signatures, after the society moved to 3")
	var st string
	fixture(t, db.QueryRow(bg(), `SELECT approval_status($1)::text`, req).Scan(&st), "status")
	if st != "approved" {
		t.Fatalf("REFUSED — the request reads %q after the quorum changed", st)
	}
}
