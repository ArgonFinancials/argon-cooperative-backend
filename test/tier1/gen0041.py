# Run from the repo root:  python3 test/tier1/gen0041.py .
# Generates test/tier1/0041_interface.sql (M0) and test/tier1/mutants_0041/*.sql
# from migrations/0041_coop_reference.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0041_coop_reference.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0041_coop_reference.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

R = {
 "argon_prefix": [("  SELECT 'COOP-' || replace(p_hold::text, '-', '')", "  SELECT 'Argon-' || replace(p_hold::text, '-', '')")],
 "vfd_bank_ok": [("ALTER TABLE member_bank_accounts ADD CONSTRAINT bank_not_at_vfd CHECK (bank_code <> '090110');\n", "")],
 # Over-strict, for the control: no bank details are taken at all.
 "c_no_bank_taken": [("CHECK (bank_code <> '090110');", "CHECK (bank_code <> '090110' AND false);")],
}
M0_OFF = ["argon_prefix", "vfd_bank_ok"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0041's INTERFACE: \"M0\" for suite 0041, GENERATED (gen0041.py) from the migration\n"
        "-- with every rule off: the wallet's Argon- prefix, and bank details at VFD taken.\n"
        "-- Applied on top of the real 0001-0040.\n")
open(os.path.join(REPO, "test/tier1/0041_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0041")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0041.py): a full copy of 0041 with\n-- one rule broken, applied over 0041_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
