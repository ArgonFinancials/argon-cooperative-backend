# Run from the repo root:  python3 test/tier1/gen0044.py .
# Generates test/tier1/0044_interface.sql (M0) and test/tier1/mutants_0044/*.sql
# from migrations/0044_period_close.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0044_period_close.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0044_period_close.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

OFF = "  IF false THEN"
R = {
 "same_officer": [("  IF NEW.decided_by = c.proposed_by THEN", OFF)],
 "rejection_closes": [("JOIN period_close_decisions k ON k.close_id = c.id AND k.confirmed\n", "JOIN period_close_decisions k ON k.close_id = c.id\n")],
 "snapshot_missing": [("             GROUP BY a2.id, a2.code, a2.name, a2.kind) t;\n", "             GROUP BY a2.id, a2.code, a2.name, a2.kind) t;\n    NEW.trial_balance := NULL;\n")],
 "future_ok": [("  IF NEW.through > society_today() THEN", OFF)],
 "backward_ok": [("  IF NEW.through <= coalesce(society_closed_through(NEW.society_id), '-infinity'::date) THEN", OFF)],
 "posting_open": [("  IF closed IS NOT NULL AND (NEW.value_date IS NULL OR NEW.value_date <= closed) THEN", "  IF closed IS NOT NULL AND NEW.value_date IS NULL THEN")],
 "undated_open": [("  IF closed IS NOT NULL AND (NEW.value_date IS NULL OR NEW.value_date <= closed) THEN", "  IF closed IS NOT NULL AND NEW.value_date <= closed THEN")],
 "unbalanced_ok": [("    IF total <> 0 THEN", "    IF false THEN")],
 "proposal_any_act": [("  IF NOT FOUND OR a.kind <> 'period_close' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "decision_any_act": [("  IF NOT FOUND OR a.kind <> 'period_close_decision' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "proposal_anyone": [("  IF NOT dispute_officer(NEW.proposed_by, NEW.society_id) THEN", OFF)],
 "decision_anyone": [("  IF NOT dispute_officer(NEW.decided_by, c.society_id) THEN", OFF)],
 "two_waiting": [("  IF EXISTS (SELECT 1 FROM period_closes c WHERE c.society_id = NEW.society_id", "  IF false AND EXISTS (SELECT 1 FROM period_closes c WHERE c.society_id = NEW.society_id")],
 "reject_silent": [(",\n  CONSTRAINT period_close_rejection_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)", "")],
 "decided_twice": [("CREATE UNIQUE INDEX period_close_decided_once ON period_close_decisions (close_id);\n", "")],
 "editable": [(l + "\n", "") for l in [
     "CREATE TRIGGER period_closes_append_only BEFORE UPDATE OR DELETE ON period_closes FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();",
     "CREATE TRIGGER period_close_decisions_append_only BEFORE UPDATE OR DELETE ON period_close_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();"]],
 # Over-strict, for the control: a closed period blocks its corrections too.
 "c_corrections_blocked": [("  IF closed IS NOT NULL AND (NEW.value_date IS NULL OR NEW.value_date <= closed) THEN",
                            "  IF closed IS NOT NULL AND (NEW.value_date IS NULL OR NEW.value_date <= closed\n     OR coalesce(NEW.reverses_id, NEW.adjusts_id) IS NOT NULL) THEN")],
}
# M0: every rule off (any decision closes, so an honest confirmation still
# does). posting_open and undated_open edit one line; M0 turns the whole
# check off.
M0_OFF = [n for n in R if not n.startswith("c_") and n not in ("posting_open", "undated_open")]
M0_EXTRA = [("  IF closed IS NOT NULL AND (NEW.value_date IS NULL OR NEW.value_date <= closed) THEN", OFF)]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

m0 = apply(body(src), M0_OFF)
for old, new in M0_EXTRA:
    m0 = rep(m0, old, new, "M0")
hdr0 = ("-- 0044's INTERFACE: \"M0\" for suite 0044, GENERATED (gen0044.py) from the migration\n"
        "-- with every rule off: a proposal closes, anyone on any act, back or forward or\n"
        "-- ahead, over any books, nothing kept, any number at once, edited, and a closed\n"
        "-- period still takes postings. Applied on top of the real 0001-0043.\n")
open(os.path.join(REPO, "test/tier1/0044_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + m0)

d = os.path.join(REPO, "test/tier1/mutants_0044")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0044.py): a full copy of 0044 with\n-- one rule broken, applied over 0044_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
