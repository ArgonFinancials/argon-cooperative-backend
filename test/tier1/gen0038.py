# Run from the repo root:  python3 test/tier1/gen0038.py .
# Generates test/tier1/0038_interface.sql (M0) and test/tier1/mutants_0038/*.sql
# from migrations/0038_member_status.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0038_member_status.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0038_member_status.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

R = {
 "not_applied": [("  UPDATE members SET status = NEW.to_status WHERE id = NEW.member_id;\n", "")],
 "any_act": [("  IF NOT FOUND OR a.kind <> 'member_status' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "any_actor": [("  IF NOT EXISTS (SELECT 1 FROM appointments ap JOIN members sm ON sm.id = ap.member_id", "  IF false AND NOT EXISTS (SELECT 1 FROM appointments ap JOIN members sm ON sm.id = ap.member_id")],
 "self_ok": [("  IF m.user_id IS NOT DISTINCT FROM a.actor_user_id THEN", "  IF false THEN")],
 "no_reason": [("  IF btrim(coalesce(NEW.reason, '')) = '' THEN", "  IF false THEN")],
 "any_move": [("  IF (m.status, NEW.to_status) NOT IN (('active', 'suspended'), ('suspended', 'active'), ('active', 'exited'), ('suspended', 'exited')) THEN", "  IF false THEN")],
 "officer_ok": [("  IF NEW.to_status IN ('suspended', 'exited') AND EXISTS (", "  IF false AND EXISTS (")],
 "editable": [("CREATE TRIGGER member_status_change_append_only BEFORE UPDATE OR DELETE ON member_status_changes FOR EACH ROW EXECUTE FUNCTION member_status_change_append_only();\n", "")],
 # Over-strict, for the control: no move is ever made.
 "c_no_moves": [("  IF (m.status, NEW.to_status) NOT IN (('active', 'suspended'), ('suspended', 'active'), ('active', 'exited'), ('suspended', 'exited')) THEN", "  IF true THEN")],
}
M0_OFF = ["not_applied", "any_act", "any_actor", "self_ok", "no_reason", "any_move", "officer_ok", "editable"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0038's INTERFACE: \"M0\" for suite 0038, GENERATED (gen0038.py) from the migration\n"
        "-- with every rule off: any act, anyone, their own, no reason, any move, an officer\n"
        "-- in post, an editable record, and the member never moved. The record exists: the\n"
        "-- control needs it. Applied on top of the real 0001-0037.\n")
open(os.path.join(REPO, "test/tier1/0038_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0038")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0038.py): a full copy of 0038 with\n-- one rule broken, applied over 0038_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
