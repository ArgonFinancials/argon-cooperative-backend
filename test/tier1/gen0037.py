# Run from the repo root:  python3 test/tier1/gen0037.py .
# Generates test/tier1/0037_interface.sql (M0) and test/tier1/mutants_0037/*.sql
# from migrations/0037_defaults_said.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0037_defaults_said.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0037_defaults_said.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

R = {
 "founding_ignores": [("signatures_required, withdrawal_request_days, guard_threshold_kobo)\n    VALUES (p_code, btrim(p_name), btrim(p_reg), btrim(p_directorate), p_withdrawal, p_write_off, p_signatures, p_request_days, p_guard_kobo)",
                       "signatures_required)\n    VALUES (p_code, btrim(p_name), btrim(p_reg), btrim(p_directorate), p_withdrawal, p_write_off, p_signatures)")],
 "days_unchecked": [("  IF p_request_days IS NULL OR p_request_days < 1 OR p_request_days > 366 THEN", "  IF p_request_days IS NULL THEN")],
 "status_defaulted": [("ALTER TABLE members ALTER COLUMN status DROP DEFAULT;\n", "")],
 "rate_defaulted": [("ALTER TABLE loan_products ALTER COLUMN interest_rate_bp DROP DEFAULT;\n", "")],
 "postable_kept": [("ALTER TABLE accounts DROP COLUMN postable;\n", "")],
 "wallets_kept": [("DROP TABLE wallet_reconciliations;\nDROP TABLE wallets;\n", "")],
 # Over-strict, for the control: every request window refused.
 "c_days_refused": [("  IF p_request_days IS NULL OR p_request_days < 1 OR p_request_days > 366 THEN", "  IF true THEN")],
}
M0_OFF = ["founding_ignores", "days_unchecked", "status_defaulted", "rate_defaulted", "postable_kept", "wallets_kept"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0037's INTERFACE: \"M0\" for suite 0037, GENERATED (gen0037.py) from the migration\n"
        "-- with every rule off: founding takes the window and the threshold and keeps the\n"
        "-- columns' defaults, unchecked; members and loan products keep their defaults;\n"
        "-- postable and the wallets tables stay. Applied on top of the real 0001-0036.\n")
open(os.path.join(REPO, "test/tier1/0037_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0037")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0037.py): a full copy of 0037 with\n-- one rule broken, applied over 0037_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
