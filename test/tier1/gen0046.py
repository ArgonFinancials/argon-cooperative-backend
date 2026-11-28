# Run from the repo root:  python3 test/tier1/gen0046.py .
# Generates test/tier1/0046_interface.sql (M0) and test/tier1/mutants_0046/*.sql
# from migrations/0046_vfd_banks.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0046_vfd_banks.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0046_vfd_banks.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

WHOLE = "CREATE CONSTRAINT TRIGGER vfd_bank_list_whole AFTER INSERT ON vfd_bank_lists\n  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION vfd_bank_list_whole();\n"
R = {
 "half_list": [(WHOLE, "")],
 "oldest_served": [("ORDER BY taken_at DESC, id DESC LIMIT 1", "ORDER BY taken_at ASC, id ASC LIMIT 1")],
 "editable": [(l + "\n", "") for l in [
     "CREATE TRIGGER vfd_bank_lists_append_only BEFORE UPDATE OR DELETE ON vfd_bank_lists\n  FOR EACH ROW EXECUTE FUNCTION vfd_banks_append_only();",
     "CREATE TRIGGER vfd_banks_append_only BEFORE UPDATE OR DELETE ON vfd_banks\n  FOR EACH ROW EXECUTE FUNCTION vfd_banks_append_only();"]],
 "twice_in_a_list": [("  CONSTRAINT vfd_bank_once_in_a_list PRIMARY KEY (list_id, code)", "  CONSTRAINT vfd_bank_any PRIMARY KEY (list_id, code, name)")],
 "nameless": [("  name    TEXT NOT NULL CONSTRAINT vfd_bank_name_said CHECK (btrim(name) <> ''),", "  name    TEXT NOT NULL,")],
 # Over-strict, for the control: no list is ever whole.
 "c_never_whole": [("  IF n <> l.bank_count THEN", "  IF true THEN")],
}
M0_OFF = ["half_list", "editable", "twice_in_a_list", "nameless"]
M0_EXTRA = [("   WHERE l.id = (SELECT id FROM vfd_bank_lists ORDER BY taken_at DESC, id DESC LIMIT 1);", ";")]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

m0 = apply(body(src), M0_OFF)
for old, new in M0_EXTRA:
    m0 = rep(m0, old, new, "M0")
hdr0 = ("-- 0046's INTERFACE: \"M0\" for suite 0046, GENERATED (gen0046.py) from the migration\n"
        "-- with every rule off: a list half written, every list served, edited, a code\n"
        "-- twice, a bank with no name. Applied on top of the real 0001-0045.\n")
open(os.path.join(REPO, "test/tier1/0046_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + m0)

d = os.path.join(REPO, "test/tier1/mutants_0046")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0046.py): a full copy of 0046 with\n-- one rule broken, applied over 0046_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
