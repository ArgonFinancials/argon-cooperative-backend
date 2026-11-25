# Run from the repo root:  python3 test/tier1/gen0040.py .
# Generates test/tier1/0040_interface.sql (M0) and test/tier1/mutants_0040/*.sql
# from migrations/0040_member_bank_accounts.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0040_member_bank_accounts.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0040_member_bank_accounts.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

SETTLED = "         coalesce(k.found AND (k.name_check = 'matches' OR c.id IS NOT NULL), false) AS name_settled"
TO_BANK = " AND b.bank_code = NEW.to_bank AND b.name_settled) THEN"
R = {
 # Who set them: the database's, never the writer's.
 "who_said": [("  NEW.by_member := (m.user_id IS NOT DISTINCT FROM a.actor_user_id);", "  NEW.by_member := true;")],
 "setter_said": [("  NEW.set_by_user := a.actor_user_id;", "  NEW.set_by_user := coalesce(NEW.set_by_user, a.actor_user_id);")],
 "any_act": [("  IF NOT FOUND OR a.kind <> 'member_bank_account' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "anyone_sets": [("    IF NOT EXISTS (SELECT 1 FROM appointments ap JOIN members om ON om.id = ap.member_id", "    IF false AND NOT EXISTS (SELECT 1 FROM appointments ap JOIN members om ON om.id = ap.member_id")],
 "auditor_sets": [(" AND ap.society_id = m.society_id AND ap.post <> 'internal_auditor'", " AND ap.society_id = m.society_id")],
 "officer_for_pin_holder": [("    IF m.user_id IS NOT NULL AND EXISTS (SELECT 1 FROM auth_credentials c WHERE c.user_id = m.user_id) THEN", "    IF false THEN")],
 "editable": [("CREATE TRIGGER member_bank_accounts_append_only BEFORE UPDATE OR DELETE ON member_bank_accounts FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();\n", "")],
 # The name: the database's judgement.
 "check_said": [("  NEW.name_check := CASE WHEN NOT NEW.found THEN NULL", "  NEW.name_check := CASE WHEN NEW.name_check IS NOT NULL THEN NEW.name_check WHEN NOT NEW.found THEN NULL")],
 "any_name_matches": [("                           THEN 'matches' ELSE 'differs' END;", "                           OR true THEN 'matches' ELSE 'differs' END;")],
 "one_word_enough": [("INTERSECT SELECT unnest(name_words(b)))) >= 2 END", "INTERSECT SELECT unnest(name_words(b)))) >= 1 END")],
 "found_without_name": [("  checked_at      TIMESTAMPTZ NOT NULL,\n  CONSTRAINT bank_name_when_found CHECK (found = (vfd_name IS NOT NULL AND btrim(vfd_name) <> '') AND found = (name_check IS NOT NULL))\n", "  checked_at      TIMESTAMPTZ NOT NULL\n")],
 "checked_twice": [("CREATE UNIQUE INDEX bank_name_checked_once ON bank_name_checks (bank_account_id);\n", "")],
 "checks_editable": [("CREATE TRIGGER bank_name_checks_append_only BEFORE UPDATE OR DELETE ON bank_name_checks FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();\n", "")],
 # A confirmation.
 "confirm_any_act": [("  IF NOT FOUND OR a.kind <> 'bank_name_confirmation' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "confirm_matching": [("  IF NOT EXISTS (SELECT 1 FROM bank_name_checks k WHERE k.bank_account_id = b.id AND k.name_check = 'differs') THEN", "  IF false THEN")],
 "confirm_anyone": [("  IF NOT person_holds_post(a.actor_user_id, b.society_id, true) THEN", "  IF false THEN")],
 "confirm_by_setter": [("  IF a.actor_user_id IN (b.set_by_user, (SELECT user_id FROM members WHERE id = b.member_id)) THEN", "  IF false THEN")],
 "confirm_no_reason": [("  reason          TEXT NOT NULL CONSTRAINT bank_name_confirmation_says_why CHECK (length(btrim(reason)) > 10),\n", "  reason          TEXT NOT NULL,\n")],
 "confirmed_twice": [("CREATE UNIQUE INDEX bank_name_confirmed_once ON bank_name_confirmations (bank_account_id);\n", "")],
 "confirmations_editable": [("CREATE TRIGGER bank_name_confirmations_append_only BEFORE UPDATE OR DELETE ON bank_name_confirmations FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();\n", "")],
 # What is settled, and which set is current.
 "settled_unchecked": [(SETTLED, SETTLED.replace(", false) AS name_settled", ", true) AS name_settled"))],
 "settled_differs": [(SETTLED, SETTLED.replace("k.name_check = 'matches' OR", "k.name_check IN ('matches', 'differs') OR"))],
 "current_oldest": [("   ORDER BY b.member_id, b.recorded_at DESC, b.id DESC;", "   ORDER BY b.member_id, b.recorded_at, b.id;")],
 # Refused before the send.
 "request_without_bank": [("  IF NEW.kind = 'withdrawal_out' AND society_on_vfd(NEW.society_id)", "  IF false AND NEW.kind = 'withdrawal_out' AND society_on_vfd(NEW.society_id)")],
 "hold_without_bank": [("  IF NOT FOUND THEN\n    PERFORM auth_refuse('payout_needs_bank_account'", "  IF false THEN\n    PERFORM auth_refuse('payout_needs_bank_account'")],
 "hold_unchecked": [("  IF NOT b.checked THEN", "  IF false THEN")],
 "hold_not_found": [("  IF NOT b.found THEN", "  IF false THEN")],
 "hold_unconfirmed": [("  IF NOT b.name_settled THEN", "  IF false THEN")],
 "setter_signs": [("              WHERE r.id = NEW.request_id AND r.kind = 'withdrawal_out' AND NOT b.by_member\n", "              WHERE false AND r.id = NEW.request_id AND r.kind = 'withdrawal_out' AND NOT b.by_member\n")],
 # Where a transfer goes.
 "transfer_anywhere": [("  IF NOT EXISTS (SELECT 1 FROM member_bank_current b JOIN withdrawal_holds h ON h.member_id = b.member_id", "  IF false AND NOT EXISTS (SELECT 1 FROM member_bank_current b JOIN withdrawal_holds h ON h.member_id = b.member_id")],
 "transfer_any_bank": [(TO_BANK, " AND b.name_settled) THEN")],
 "transfer_unsettled": [(TO_BANK, " AND b.bank_code = NEW.to_bank) THEN")],
 # Over-strict, for the control: no name is ever settled.
 "c_never_settled": [(SETTLED, "         false AS name_settled")],
}
M0_OFF = ["who_said", "setter_said", "any_act", "anyone_sets", "auditor_sets", "officer_for_pin_holder", "editable",
          "check_said", "any_name_matches", "one_word_enough", "found_without_name", "checked_twice", "checks_editable",
          "confirm_any_act", "confirm_matching", "confirm_anyone", "confirm_by_setter", "confirm_no_reason", "confirmed_twice",
          "confirmations_editable", "settled_unchecked", "current_oldest", "request_without_bank", "hold_without_bank",
          "hold_unchecked", "hold_not_found", "hold_unconfirmed", "setter_signs", "transfer_anywhere"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0040's INTERFACE: \"M0\" for suite 0040, GENERATED (gen0040.py) from the migration\n"
        "-- with every rule off: anyone sets anyone's bank details on any act, said by the\n"
        "-- writer, edited; any name matches, as the writer says; checks and confirmations\n"
        "-- any number of times, by anyone, edited; nothing refused at the request or the\n"
        "-- hold; a transfer anywhere. Applied on top of the real 0001-0039.\n")
open(os.path.join(REPO, "test/tier1/0040_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0040")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0040.py): a full copy of 0040 with\n-- one rule broken, applied over 0040_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
