//go:build tier1

// ACCEPTANCE TESTS — 0046, THE BANKS AS VFD LISTS THEM (2026-11-28, the
// reviewer: "VFD's own list, cached"). A list and its banks are written in
// one transaction and hold exactly what the list says; the newest list is the
// one served; nothing is edited.
package tier1

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// takeBanks: a list saying `say` banks, with `banks` (code, name pairs) in it.
func takeBanks(say int, banks ...[2]string) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		if err := x.QueryRow(bg(), `INSERT INTO vfd_bank_lists (bank_count) VALUES ($1) RETURNING id::text`, say).Scan(&id); err != nil {
			return err
		}
		for _, b := range banks {
			if _, err := x.Exec(bg(), `INSERT INTO vfd_banks (list_id, code, name) VALUES ($1, $2, $3)`, id, b[0], b[1]); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func servedBanks(t *testing.T) map[string]string {
	t.Helper()
	rows, err := db.Query(bg(), `SELECT code, name FROM vfd_banks_current`)
	fixture(t, err, "the banks served")
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, n string
		fixture(t, rows.Scan(&c, &n), "a bank")
		out[c] = n
	}
	return out
}

func bankCode() string { return fmt.Sprintf("9%05d", seq.Add(1)%100000) }

func TestVfdBanks01_AListHoldsTheBanksItSays(t *testing.T) {
	a, b := bankCode(), bankCode()
	_, err := takeBanks(3, [2]string{a, "FIRST BANK"}, [2]string{b, "GTBANK"})
	refused(t, err, "a list saying 3 banks committed with 2 (a list half written)", refusedAs("vfd_bank_list_whole"))
}

func TestVfdBanks02_TheNewestListIsServed(t *testing.T) {
	old, fresh := bankCode(), bankCode()
	_, err := takeBanks(1, [2]string{old, "OLD BANK"})
	fixture(t, err, "the older list")
	time.Sleep(20 * time.Millisecond) // the lists' times differ
	_, err = takeBanks(1, [2]string{fresh, "NEW BANK"})
	fixture(t, err, "the newer list")
	got := servedBanks(t)
	if got[fresh] != "NEW BANK" || got[old] != "" {
		t.Fatalf("NOT THE NEWEST — the banks served are %v; want only the newest list's (%s)", got, fresh)
	}
}

func TestVfdBanks03_AListIsNeverEdited(t *testing.T) {
	c := bankCode()
	id, err := takeBanks(1, [2]string{c, "A BANK"})
	fixture(t, err, "a list")
	refused(t, one(`UPDATE vfd_banks SET name = 'ANOTHER BANK' WHERE list_id = $1`, id), "a bank's name rewritten", refusedAs("vfd_bank_list_append_only"))
	refused(t, one(`DELETE FROM vfd_bank_lists WHERE id = $1`, id), "a list removed", refusedAs("vfd_bank_list_append_only"))
}

func TestVfdBanks04_EachBankIsSaidAndOnceInAList(t *testing.T) {
	c := bankCode()
	_, err := takeBanks(2, [2]string{c, "A BANK"}, [2]string{c, "A BANK AGAIN"})
	refused(t, err, "one code twice in a list", want{codes: []string{"23505"}, constraint: "vfd_bank_once_in_a_list"})
	_, err = takeBanks(1, [2]string{bankCode(), " "})
	refused(t, err, "a bank with no name", refusedAs("vfd_bank_name_said"))
}

func TestVfdBanks_Control_AWholeListIsTaken(t *testing.T) {
	a, b := bankCode(), bankCode()
	_, err := takeBanks(2, [2]string{a, "FIRST BANK"}, [2]string{b, "GTBANK"})
	accepted(t, err, "a list of two banks, both in it")
}
