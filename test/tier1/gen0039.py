# Run from the repo root:  python3 test/tier1/gen0039.py .
# Generates test/tier1/0039_interface.sql (M0) and test/tier1/mutants_0039/*.sql
# from migrations/0039_vfd_as_proven.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0039_vfd_as_proven.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0039_vfd_as_proven.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

PAID = "  IF NEW.result = 'paid' AND NOT ((ev.kind = 'sent' AND ev.result = 'accepted' AND ev.code = '00')\n                               OR (ev.kind = 'requeried' AND ev.result = 'succeeded' AND ev.code = '00' AND ev.txn_status = '00')) THEN"
FAILED = "    IF NOT coalesce(ev.kind = 'requeried' AND ev.result = 'not_found' AND ev.code = '108' AND ev.at >= sent + vfd_settlement_window()\n            AND prev.result = 'not_found' AND prev.code = '108' AND prev.at >= sent + vfd_settlement_window(), false) THEN"
R = {
 "paid_on_any_send": [(PAID, PAID.replace("(ev.kind = 'sent' AND ev.result = 'accepted' AND ev.code = '00')", "(ev.kind = 'sent')"))],
 "paid_any_txn": [(PAID, PAID.replace(" AND ev.txn_status = '00'", ""))],
 "failed_once": [(FAILED, FAILED.replace("\n            AND prev.result = 'not_found' AND prev.code = '108' AND prev.at >= sent + vfd_settlement_window()", ""))],
 "failed_first_early": [(FAILED, FAILED.replace(" AND prev.at >= sent + vfd_settlement_window()", ""))],
 "failed_any_code": [(FAILED, FAILED.replace(" AND ev.code = '108'", "").replace(" AND prev.code = '108'", ""))],
 "failed_on_send": [(FAILED, FAILED.replace("    IF NOT coalesce(", "    IF NOT (ev.kind = 'sent' AND ev.result IN ('refused', 'not_reached')) AND NOT coalesce("))],
 "from_anywhere": [("  IF NOT EXISTS (SELECT 1 FROM vfd_pool WHERE account_number = NEW.from_account) THEN", "  IF false THEN")],
 "pool_twice": [("CREATE UNIQUE INDEX vfd_pool_once ON vfd_pool ((true));\n", "")],
 "pool_editable": [("CREATE TRIGGER vfd_pool_append_only BEFORE UPDATE OR DELETE ON vfd_pool FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();\n", "")],
 "needs_vfd_unseen": [("   WHERE ev.result = 'pending'\n", "   WHERE false\n")],
 "posts_only_vfds_amount": [("coalesce(ev.amount_kobo, t.amount_kobo) + h.partner_fee_kobo", "ev.amount_kobo + h.partner_fee_kobo"), ("-coalesce(ev.amount_kobo, t.amount_kobo), NULL::uuid", "-ev.amount_kobo, NULL::uuid")],
 "source_optional": [("ALTER TABLE vfd_notifications ALTER COLUMN source SET NOT NULL;\n", "")],
 "source_any": [("ALTER TABLE vfd_notifications ADD CONSTRAINT vfd_notification_source_known CHECK (source IN ('webhook', 'history'));\n", "")],
 # Over-strict, for the control: nothing ever fails.
 "c_never_failed": [(FAILED, FAILED.replace("    IF NOT coalesce(", "    IF true OR NOT coalesce("))],
}
# M0: the paid, failed and source rules each edit shared lines; M0 takes one
# of each, which turns every test red.
M0_OFF = ["paid_on_any_send", "failed_once", "from_anywhere", "pool_twice", "pool_editable", "needs_vfd_unseen", "posts_only_vfds_amount", "source_optional", "source_any"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0039's INTERFACE: \"M0\" for suite 0039, GENERATED (gen0039.py) from the migration\n"
        "-- with every rule off: paid on any send, failed on one 108, from any account, any\n"
        "-- number of pools and edited, nothing shown for a person, a send's payout unposted,\n"
        "-- a notification from anywhere or nowhere. Applied on top of the real 0001-0038.\n")
open(os.path.join(REPO, "test/tier1/0039_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0039")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0039.py): a full copy of 0039 with\n-- one rule broken, applied over 0039_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
