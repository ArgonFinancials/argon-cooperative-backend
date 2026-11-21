# Run from the repo root:  python3 test/tier1/gen0035.py .
# Generates test/tier1/0035_interface.sql (M0) and test/tier1/mutants_0035/*.sql
# from migrations/0035_demo_societies.up.sql. Every replacement must apply
# exactly once, or the run stops.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0035_demo_societies.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0035_demo_societies.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

R = {
 "demo_insertable": [("    IF NEW.demo THEN\n      PERFORM auth_refuse('demo_by_its_function'", "    IF false THEN\n      PERFORM auth_refuse('demo_by_its_function'")],
 "demo_unmarked": [("  UPDATE societies SET demo = true WHERE id = v_id;\n", "")],
 "demo_reversible": [("  IF OLD.demo AND NOT NEW.demo THEN", "  IF false THEN")],
 "demo_any_society": [("    IF EXISTS (SELECT 1 FROM members WHERE society_id = NEW.id) THEN", "    IF false THEN")],
 "demo_any_name": [("  IF NEW.demo AND NEW.registered_name NOT LIKE 'TEST DATA%' THEN", "  IF false THEN")],
 "pin_for_anyone": [("  IF NOT auth_demo_only(p_user) THEN", "  IF false THEN")],
 "demo_only_any_membership": [("  SELECT EXISTS (SELECT 1 FROM members m WHERE m.user_id = p_user)\n     AND NOT EXISTS (SELECT 1 FROM members m JOIN societies s ON s.id = m.society_id WHERE m.user_id = p_user AND NOT s.demo);",
                                "  SELECT EXISTS (SELECT 1 FROM members m JOIN societies s ON s.id = m.society_id WHERE m.user_id = p_user AND s.demo);")],
 "demo_only_nobody": [("  SELECT EXISTS (SELECT 1 FROM members m WHERE m.user_id = p_user)\n     AND NOT EXISTS", "  SELECT NOT EXISTS")],
 "pin_unattributed": [("  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;\n  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;\n  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'setting a PIN for someone says why'); END IF;\n", "")],
 # Over-strict, for the control: nobody is ever demonstration-only.
 "c_nobody_demo": [("  SELECT EXISTS (SELECT 1 FROM members m WHERE m.user_id = p_user)\n     AND NOT EXISTS", "  SELECT false AND NOT EXISTS")],
}
# M0: every rule off. demo_only_* edit the same lines as each other and the
# control; M0 takes pin_for_anyone instead, which turns their tests red too.
M0_OFF = ["demo_insertable", "demo_reversible", "demo_any_society", "demo_any_name", "pin_for_anyone", "pin_unattributed"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0035's INTERFACE: \"M0\" for suite 0035, GENERATED (gen0035.py) from the migration\n"
        "-- with every rule off: a society is inserted marked, unmarked again, marked with\n"
        "-- people in it and any name; the operator sets anyone's PIN, as itself, naming\n"
        "-- nobody. The mark, the function and the PIN still exist: the control needs them.\n"
        "-- Applied on top of the real 0001-0034.\n")
open(os.path.join(REPO, "test/tier1/0035_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0035")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0035.py): a full copy of 0035 with\n-- one rule broken, applied over 0035_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
