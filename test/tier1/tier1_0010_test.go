//go:build tier1

// Suite 0010 — A SIGNATURE AND A REJECTION REST ON THE SIGNER'S OWN STEP-UP
// (decided 2026-10-07). Written before the migration, for review.
//
// As a batch rests on its actor's act (0005): each signature and each
// rejection names a credential act that is the person's OWN, of the right
// kind, written in the SAME transaction (so the act and the signature are one
// event), and used ONCE. The act itself already needs the person's PIN,
// entered in the last five minutes, minted in another transaction (0003, 0005).
package tier1

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// actAndSign: `actor` takes a step-up of `kind` and, in ONE transaction,
// records the act and the signature (by `member` in `post`).
func (w *aworld) actAndSign(t *testing.T, actor, kind, req, member, post string) error {
	proof := stepUp(t, actor, kind)
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
			kind, actor, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, $3, $4)`,
			req, member, post, act)
		return err
	})
}

func (w *aworld) actAndReject(t *testing.T, actor, kind, req, member, user, post, reason string) error {
	proof := stepUp(t, actor, kind)
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
			kind, actor, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, note, act_id)
			VALUES ($1, $2, $3, $4, $5, 'The payslip is not attached', $6)`, req, member, user, post, reason, act)
		return err
	})
}

// anAct: a committed act of `kind` by `actor`, for the tests that need one
// from an earlier transaction.
func anAct(t *testing.T, actor, kind string) string {
	t.Helper()
	proof := stepUp(t, actor, kind)
	var act string
	fixture(t, db.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
		kind, actor, proof).Scan(&act), "a step-up act")
	return act
}

// =============================================================================
// SIGNATURES
// =============================================================================

func TestSignStep1_ASignatureRestsOnAStepUp(t *testing.T) {
	w := newApprovals(t)
	refused(t, one(`INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($1, $2, 'treasurer')`, w.s.request(), w.tMem),
		"a signature with no step-up behind it", refusedAs("signature_needs_step_up"))
}

func TestSignStep2_TheActIsTheSignersOwn(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	proof := stepUp(t, w.chairman, "approval_signature")
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('approval_signature', $1, $2) RETURNING id`,
			w.chairman, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, 'treasurer', $3)`,
			req, w.tMem, act)
		return err
	})
	refused(t, err, "the treasurer's signature resting on the chairman's step-up", refusedAs("signature_act_is_signers"))
}

func TestSignStep3_TheActIsASignatureAct(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.actAndSign(t, w.treasurer, "ledger_batch", w.s.request(), w.tMem, "treasurer"),
		"a signature resting on the treasurer's step-up for an import", refusedAs("signature_act_kind"))
}

// The act and the signature are one event: an act from an earlier
// transaction could be reused by a later, unseen signature.
func TestSignStep4_TheActIsFromThisTransaction(t *testing.T) {
	w := newApprovals(t)
	act := anAct(t, w.treasurer, "approval_signature")
	refused(t, one(`INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, 'treasurer', $3)`,
		w.s.request(), w.tMem, act), "a signature resting on an act committed earlier", refusedAs("signature_act_same_transaction"))
}

func TestSignStep5_OneStepUpSignsOnce(t *testing.T) {
	w := newApprovals(t)
	a, b := w.s.request(), w.s.request()
	proof := stepUp(t, w.treasurer, "approval_signature")
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('approval_signature', $1, $2) RETURNING id`,
			w.treasurer, proof).Scan(&act); err != nil {
			return err
		}
		if _, err := x.Exec(bg(), `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, 'treasurer', $3)`, a, w.tMem, act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, 'treasurer', $3)`, b, w.tMem, act)
		return err
	})
	refused(t, err, "one step-up signing two requests", uniqueOrCheck("signature_act_once"))
}

// =============================================================================
// REJECTIONS
// =============================================================================

func TestSignStep6_ARejectionRestsOnAStepUp(t *testing.T) {
	w := newApprovals(t)
	// Written out, not through w.reject: since the fixtures moved onto step-up
	// acts, w.reject supplies one, and this test would send what the rule
	// accepts (finding 27).
	refused(t, one(`INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, note)
		VALUES ($1, $2, $3, 'treasurer', 'amount_wrong', 'The payslip is not attached')`, w.s.request(), w.tMem, w.treasurer),
		"a rejection with no step-up behind it", refusedAs("rejection_needs_step_up"))
}

func TestSignStep7_TheActIsTheRejectersOwn(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.actAndReject(t, w.chairman, "approval_rejection", w.s.request(), w.tMem, w.treasurer, "treasurer", "amount_wrong"),
		"the treasurer's rejection resting on the chairman's step-up", refusedAs("rejection_act_is_rejecters"))
}

func TestSignStep8_TheActIsARejectionAct(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.actAndReject(t, w.treasurer, "approval_signature", w.s.request(), w.tMem, w.treasurer, "treasurer", "amount_wrong"),
		"a rejection resting on a step-up taken to SIGN", refusedAs("rejection_act_kind"))
}

func TestSignStep9_TheRejectionActIsFromThisTransaction(t *testing.T) {
	w := newApprovals(t)
	act := anAct(t, w.treasurer, "approval_rejection")
	refused(t, one(`INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, act_id)
		VALUES ($1, $2, $3, 'treasurer', 'amount_wrong', $4)`, w.s.request(), w.tMem, w.treasurer, act),
		"a rejection resting on an act committed earlier", refusedAs("rejection_act_same_transaction"))
}

func TestSignStep10_OneStepUpRejectsOnce(t *testing.T) {
	w := newApprovals(t)
	a, b := w.s.request(), w.s.request()
	proof := stepUp(t, w.treasurer, "approval_rejection")
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('approval_rejection', $1, $2) RETURNING id`,
			w.treasurer, proof).Scan(&act); err != nil {
			return err
		}
		q := `INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, act_id) VALUES ($1, $2, $3, 'treasurer', 'amount_wrong', $4)`
		if _, err := x.Exec(bg(), q, a, w.tMem, w.treasurer, act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), q, b, w.tMem, w.treasurer, act)
		return err
	})
	refused(t, err, "one step-up rejecting two requests", uniqueOrCheck("rejection_act_once"))
}

// =============================================================================
// CONTROLS
// =============================================================================

func TestSignStep_Control_SigningWithTheirOwnStepUp(t *testing.T) {
	w := newApprovals(t)
	accepted(t, w.actAndSign(t, w.treasurer, "approval_signature", w.s.request(), w.tMem, "treasurer"),
		"the treasurer signs, resting on their own signature step-up, in one transaction")
}

func TestSignStep_Control_RejectingWithTheirOwnStepUp(t *testing.T) {
	w := newApprovals(t)
	accepted(t, w.actAndReject(t, w.treasurer, "approval_rejection", w.s.request(), w.tMem, w.treasurer, "treasurer", "documents_missing"),
		"the treasurer rejects, resting on their own rejection step-up, in one transaction")
}
