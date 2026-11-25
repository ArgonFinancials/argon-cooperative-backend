//go:build tier1

// ACCEPTANCE TESTS — 0041, THE COOPERATIVE'S OWN TRANSFER REFERENCE, AND NO
// PAYOUT INTO AN ACCOUNT AT VFD (2026-11-25, the reviewer, on the wallet
// platform's review: both platforms send from one VFD pool).
//
//   - a transfer's reference is "COOP-" and the hold's 32 hex characters,
//     never the wallet's "Argon-": the prefix says whose a transfer is;
//   - a member's bank details are never at VFD's own bank code (090110): a
//     payout into an account on the same credentials never leaves the pool.
package tier1

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestCoopRef01_ATransferReferenceIsTheCooperativesOwn(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	want := "COOP-" + strings.ReplaceAll(hold, "-", "")
	var ref string
	fixture(t, db.QueryRow(bg(), `SELECT vfd_transfer_reference($1)`, hold).Scan(&ref), "the reference")
	if ref != want {
		t.Fatalf("WRONG REFERENCE — %q; want %q: the wallet platform sends \"Argon-\" from the same pool, and the prefix is what tells them apart", ref, want)
	}
	send := func(r string) error {
		return tx(func(x pgx.Tx) error {
			_, err := x.Exec(bg(), `INSERT INTO vfd_transfers (hold_id, reference, from_account, to_account, amount_kobo, to_bank)
				VALUES ($1, $2, $3, $4, $5, $6)`, hold, r, ensurePool(), w.memAcct, payKobo, testBank)
			return err
		})
	}
	refused(t, send("Argon-"+strings.ReplaceAll(hold, "-", "")), "a transfer under the wallet platform's prefix", refusedAs("vfd_reference_is_the_holds"))
	accepted(t, send(want), "a transfer under the cooperative's own reference")
}

func TestCoopRef02_NoBankDetailsAtVFD(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	_, err := setBank(t, bankSet{actor: user, member: mem, bank: "090110"})
	refused(t, err, "a member's bank details at VFD's own bank code (a payout there may never leave the pool)", refusedAs("bank_not_at_vfd"))
}

func TestCoopRef_Control_AnotherBanksDetailsAreTaken(t *testing.T) {
	w := newMoneyWorld(t) // no bank details of its own: the control's set is the first
	user, mem := bankMember(t, w, "Folake Adebayo", true)
	_, err := setBank(t, bankSet{actor: user, member: mem, bank: "000014"})
	accepted(t, err, "a member's bank details at another bank")
}
