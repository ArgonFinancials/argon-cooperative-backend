# Run from the repo root:  python3 test/tier1/gen0043.py .
# Generates test/tier1/0043_interface.sql (M0) and test/tier1/mutants_0043/*.sql
# from migrations/0043_corrections.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0043_corrections.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0043_corrections.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

OFF = "  IF false THEN"
R = {
 "same_officer": [("  IF NEW.decided_by = c.proposed_by THEN", OFF)],
 "not_posted": [("    NEW.entry_id := entry_correction_post(c.id, NEW.decided_act, NEW.decided_by);\n", "")],
 "unbalanced_ok": [("    IF total <> 0 THEN", "    IF false THEN")],
 "zero_line_ok": [("      IF coalesce((l->>'amount_kobo')::bigint, 0) = 0 THEN", "      IF false THEN")],
 "any_account": [("      IF NOT EXISTS (SELECT 1 FROM ledger_lines x WHERE x.entry_id = o.id AND x.account_id = (l->>'account_id')::uuid\n                      AND x.member_id IS NOT DISTINCT FROM (l->>'member_id')::uuid) THEN",
                   "      IF NOT EXISTS (SELECT 1 FROM ledger_lines x WHERE x.entry_id = o.id AND x.account_id = (l->>'account_id')::uuid) THEN")],
 "any_member": [("\n                      AND x.member_id IS NOT DISTINCT FROM (l->>'member_id')::uuid) THEN", ") THEN")],
 "proposal_any_act": [("  IF NOT FOUND OR a.kind <> 'entry_correction' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "decision_any_act": [("  IF NOT FOUND OR a.kind <> 'entry_correction_decision' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "proposal_anyone": [("  IF NOT dispute_officer(NEW.proposed_by, o.society_id) THEN", OFF)],
 "decision_anyone": [("  IF NOT dispute_officer(NEW.decided_by, c.society_id) THEN", OFF)],
 "proposal_own": [("  IF entry_names_user(o.id, NEW.proposed_by) THEN", OFF)],
 "decision_own": [("  IF entry_names_user(c.entry_id, NEW.decided_by) THEN", OFF)],
 "two_waiting": [("  IF EXISTS (SELECT 1 FROM entry_corrections c WHERE c.entry_id = o.id", "  IF false AND EXISTS (SELECT 1 FROM entry_corrections c WHERE c.entry_id = o.id")],
 "reject_silent": [(",\n  CONSTRAINT entry_correction_rejection_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)", "")],
 "decided_twice": [("CREATE UNIQUE INDEX entry_correction_decided_once ON entry_correction_decisions (correction_id);\n", "")],
 "correct_anything": [("  IF NOT dispute_correctable(o.id) THEN", OFF)],
 "editable": [(l + "\n", "") for l in [
     "CREATE TRIGGER entry_corrections_append_only BEFORE UPDATE OR DELETE ON entry_corrections FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();",
     "CREATE TRIGGER entry_correction_decisions_append_only BEFORE UPDATE OR DELETE ON entry_correction_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();"]],
 # Over-strict, for the control: no proposal is ever taken.
 "c_no_proposals": [("  IF NOT dispute_officer(NEW.proposed_by, o.society_id) THEN", "  IF true THEN")],
}
# M0: every rule off. any_member edits any_account's lines; M0 takes any_account.
M0_OFF = [n for n in R if not n.startswith("c_") and n != "any_member"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0043's INTERFACE: \"M0\" for suite 0043, GENERATED (gen0043.py) from the migration\n"
        "-- with every rule off: anyone proposes and decides, on any act, their own entries,\n"
        "-- anything, unbalanced, anywhere, any number of times, edited, and nothing posted.\n"
        "-- Applied on top of the real 0001-0042.\n")
open(os.path.join(REPO, "test/tier1/0043_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0043")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0043.py): a full copy of 0043 with\n-- one rule broken, applied over 0043_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
