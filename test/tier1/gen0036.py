# Run from the repo root:  python3 test/tier1/gen0036.py .
# Generates test/tier1/0036_interface.sql (M0) and test/tier1/mutants_0036/*.sql
# from migrations/0036_founding_officers_active.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0036_founding_officers_active.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0036_founding_officers_active.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

R = {
 "founding_pending": [("btrim(p_phone), 'active', 'cash', p_joined, v_user", "btrim(p_phone), 'pending', 'cash', p_joined, v_user")],
 "backfill_nothing": [("     AND m.status = 'pending';", "     AND m.status = 'pending' AND false;")],
 "backfill_any_status": [("     AND m.status = 'pending';", "     AND true;")],
 # Over-strict, for the control: every pending member, founding officer or not.
 "c_backfill_everyone": [("    FROM society_settings_log l\n   WHERE l.setting = 'founding_officer' AND l.society_id = m.society_id\n     AND m.member_number = substr(l.new_value, strpos(l.new_value, ':') + 1)\n     AND m.status = 'pending';",
                          "   WHERE m.status = 'pending';")],
}
M0_OFF = ["founding_pending", "backfill_nothing"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0036's INTERFACE: \"M0\" for suite 0036, GENERATED (gen0036.py) from the migration\n"
        "-- with every rule off: a founding officer is pending, and the backfill makes nobody\n"
        "-- active. The backfill function exists: the control needs it. Applied on top of\n"
        "-- the real 0001-0035.\n")
open(os.path.join(REPO, "test/tier1/0036_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0036")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0036.py): a full copy of 0036 with\n-- one rule broken, applied over 0036_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
