//go:build tier1

// ACCEPTANCE TESTS — migration 0005: the PIN step-up on postAll, and proofs
// scoped to the act they authorise. Written before 0005, for review.
//
// What the database must make true (the user's terms, 2026-10-01):
//
//   - A PIN proof is minted FOR one act kind, bound at mint. A proof minted
//     for an import does not satisfy a reversal, or a PIN change.
//   - A proof cannot be minted and used in one transaction. If the code path
//     that consumes a proof could also mint it, the step-up would be
//     decorative. (The other half of that separation is by ROLE: the API's
//     role cannot mint at all. Privileges are the probe's to test —
//     deploy/70-verify-tier1.sh, P11–P14 — because they live in grants.sql,
//     not in a migration.)
//   - A ledger batch is authorised by a 'ledger_batch' act of the batch's own
//     actor, written in the same transaction — one act, one batch — and that
//     act, like every attributable act (0003), consumes the actor's fresh,
//     unused PIN proof.
//
// Not tested here, because the database cannot get it wrong: a batch that
// rolls back leaves its proof unconsumed (the consumption rolls back with
// it). Whether a HANDLER consumes a proof outside the batch's transaction is
// an API question: TestApiStepUp10–11.
package tier1

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// mint: a PIN proof for an act kind, in its own transaction (as the auth
// service would mint it, before the act that uses it).
func (a *authT) mint(user, kind string) string {
	a.t.Helper()
	var id string
	a.scan("proof for "+kind, `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', $2) RETURNING id`, []any{&id}, user, kind)
	return id
}

// batchIn writes a ledger batch for actor, authorised by act (may be ""), in x.
func (a *authT) batchIn(x pgx.Tx, actor, act string) error {
	_, err := x.Exec(bg(), `INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo, authorised_by)
		VALUES ($1, $2, $3, 1, 100, $4)`, a.s.id, newKey(), actor, nz(act))
	return err
}

// actIn writes a 'ledger_batch' act in x, consuming proof.
func (a *authT) actIn(x pgx.Tx, actor, proof string) (string, error) {
	var id string
	err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('ledger_batch', $1, $2) RETURNING id`,
		actor, proof).Scan(&id)
	return id, err
}

func (a *authT) secretary() person {
	a.t.Helper()
	p := a.officer("Secretary", "secretary", true)
	a.withPIN(p.user)
	return p
}

// =============================================================================
// THE PROOF IS SCOPED AT MINT
// =============================================================================

func TestProof1_PinProofNamesItsActAtMint(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	refused(t, a.exec(`INSERT INTO auth_proofs (user_id, method) VALUES ($1, 'pin')`, p.user),
		"a PIN proof minted for no act in particular", w("23514", "pin_proof_names_act"))
}

func TestProof2_ActKindMustBeKnown(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	refused(t, a.exec(`INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', 'anything at all')`, p.user),
		"a PIN proof minted for an act that does not exist", w("23514", "proof_act_kind_known"))
}

func TestProof3_ProofForAnImportDoesNotAuthoriseAPinChange(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	here := a.session(p.user, a.device(p.user))
	_, err := a.act(act{kind: "pin_change", actor: p.user, actorSession: here.id, proof: a.mint(p.user, "ledger_batch"),
		targetUser: p.user, newPin: argonHash})
	refused(t, err, "a proof minted for an import, used for a PIN change", w("23514", "act_proof_for_this_act"))
}

func TestProof3_ProofForAPinChangeDoesNotAuthoriseAnImport(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	proof := a.mint(p.user, "pin_change")
	refused(t, a.tx(func(x pgx.Tx) error {
		act, err := a.actIn(x, p.user, proof)
		if err != nil {
			return err
		}
		return a.batchIn(x, p.user, act)
	}), "a proof minted for a PIN change, used for an import", w("23514", "act_proof_for_this_act"))
}

// =============================================================================
// THE PATH THAT CONSUMES A PROOF CANNOT MINT IT
// =============================================================================

func TestProof4_ProofCannotBeMintedAndUsedInOneTransaction(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	refused(t, a.tx(func(x pgx.Tx) error {
		var proof string
		if err := x.QueryRow(bg(), `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', 'ledger_batch') RETURNING id`,
			p.user).Scan(&proof); err != nil {
			return err
		}
		act, err := a.actIn(x, p.user, proof)
		if err != nil {
			return err
		}
		return a.batchIn(x, p.user, act)
	}), "a proof minted and consumed in the same transaction — the step-up decorating itself", w("23514", "proof_minted_elsewhere"))
}

// =============================================================================
// A BATCH IS AUTHORISED BY ITS OWN ACT
// =============================================================================

func TestProof5_BatchNeedsAnAuthorisingAct(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	refused(t, a.tx(func(x pgx.Tx) error { return a.batchIn(x, p.user, "") }),
		"a ledger batch with no authorising act", w("23514", "batch_needs_act"))
}

func TestProof6_BatchActIsALedgerBatchAct(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	here := a.session(p.user, a.device(p.user))
	proof := a.mint(p.user, "pin_change") // before the transaction, as the auth service mints: not the thing under test
	refused(t, a.tx(func(x pgx.Tx) error {
		var other string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, actor_session_id, proof_id, target_user_id, new_pin_hash)
			VALUES ('pin_change', $1, $2, $3, $1, $4) RETURNING id`, p.user, here.id, proof, argonHash).Scan(&other); err != nil {
			return err
		}
		return a.batchIn(x, p.user, other)
	}), "a batch 'authorised' by the actor's PIN change", w("23514", "batch_act_is_ledger_batch"))
}

func TestProof7_BatchActIsTheActorsOwn(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	q := a.officer("Treasurer", "treasurer", true) // one current secretary per society (0001)
	a.withPIN(q.user)
	proof := a.mint(p.user, "ledger_batch")
	refused(t, a.tx(func(x pgx.Tx) error {
		act, err := a.actIn(x, p.user, proof)
		if err != nil {
			return err
		}
		return a.batchIn(x, q.user, act)
	}), "a batch posted by one person on another's act", w("23514", "batch_act_by_actor"))
}

func TestProof8_BatchActIsFromThisTransaction(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	proof := a.mint(p.user, "ledger_batch")
	var act string
	fixture(t, a.tx(func(x pgx.Tx) (err error) { act, err = a.actIn(x, p.user, proof); return err }), "an act committed on its own")
	refused(t, a.tx(func(x pgx.Tx) error { return a.batchIn(x, p.user, act) }),
		"a batch resting on an act committed earlier — a stored act reused", w("23514", "batch_act_same_transaction"))
}

func TestProof9_OneActAuthorisesOneBatch(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	proof := a.mint(p.user, "ledger_batch")
	refused(t, a.tx(func(x pgx.Tx) error {
		act, err := a.actIn(x, p.user, proof)
		if err != nil {
			return err
		}
		if err := a.batchIn(x, p.user, act); err != nil {
			return err
		}
		return a.batchIn(x, p.user, act)
	}), "two batches on one act", w("23505", "ledger_batches_authorised_by_key"))
}

// =============================================================================
// CONTROLS — the normal paths still commit
// =============================================================================

func TestProof_Control_ScopedProofAuthorisesItsBatch(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	proof := a.mint(p.user, "ledger_batch")
	accepted(t, a.tx(func(x pgx.Tx) error {
		act, err := a.actIn(x, p.user, proof)
		if err != nil {
			return err
		}
		return a.batchIn(x, p.user, act)
	}), "the secretary's batch, on an act consuming her own fresh import proof")
	var consumed bool
	a.scan("proof", `SELECT consumed_at IS NOT NULL FROM auth_proofs WHERE id = $1`, []any{&consumed}, proof)
	if !consumed {
		t.Fatalf("UNCONSUMED — the proof authorised a batch and can still be used")
	}
}

func TestProof_Control_ExistingActWithItsScopedProof(t *testing.T) {
	a := newAuth(t)
	p := a.secretary()
	here := a.session(p.user, a.device(p.user))
	_, err := a.act(act{kind: "pin_change", actor: p.user, actorSession: here.id, proof: a.mint(p.user, "pin_change"),
		targetUser: p.user, newPin: argonHash})
	accepted(t, err, "a PIN change on a proof minted for a PIN change")
}
