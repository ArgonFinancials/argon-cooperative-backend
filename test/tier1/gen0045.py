# Run from the repo root:  python3 test/tier1/gen0045.py .
# Generates test/tier1/0045_interface.sql (M0) and test/tier1/mutants_0045/*.sql
# from migrations/0045_resets_and_phone_changes.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0045_resets_and_phone_changes.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0045_resets_and_phone_changes.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

OFF = "    IF false THEN"
SEC = "    IF NOT EXISTS (SELECT 1 FROM members tm WHERE tm.user_id = target AND society_secretary(NEW.actor_user_id, tm.society_id)) THEN"
RESET_TWO = "    IF person_holds_post(target, NULL, false) THEN\n      PERFORM auth_refuse('signatory_reset_needs_two'"
PHONE_TWO = "    IF person_holds_post(target, soc, false) THEN\n      PERFORM auth_refuse('signatory_phone_needs_two'"
REQ_TARGET = "    IF NOT person_holds_post(target, soc, false) THEN\n      PERFORM auth_refuse('signatory_reset_needs_two', 'this path"
REQ_PRESENT = "    IF NEW.kind = 'signatory_phone_request' AND NEW.member_present IS NOT TRUE THEN"
NOTICE_TRIGGER = "CREATE TRIGGER members_phone_change_notice AFTER UPDATE OF phone ON members\n  FOR EACH ROW EXECUTE FUNCTION phone_change_notice();\n"
R = {
 "reset_any_officer": [(SEC, OFF)],
 "officer_one_reset": [(RESET_TWO, RESET_TWO.replace("NULL, false)", "NULL, true)"))],
 "request_signing_only": [(REQ_TARGET, REQ_TARGET.replace("soc, false)", "soc, true)"))],
 "approval_silent": [("    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN\n      PERFORM auth_refuse('approval_states_reason'",
                      OFF + "\n      PERFORM auth_refuse('approval_states_reason'")],
 "phone_any_officer": [("    IF NOT society_secretary(NEW.actor_user_id, soc) THEN", OFF)],
 "phone_absent_ok": [("    IF NEW.member_present IS NOT TRUE THEN", OFF)],
 "officer_phone_one": [(PHONE_TWO, PHONE_TWO.replace("soc, false)", "soc, true)"))],
 "request_absent_ok": [(REQ_PRESENT, OFF)],
 "notice_missing": [(NOTICE_TRIGGER, "")],
 "notice_one_path": [("  IF act IS NULL THEN\n    SELECT a.id INTO act", "  IF false THEN\n    SELECT a.id INTO act")],
 "sent_twice": [("CREATE UNIQUE INDEX phone_notice_sent_once ON phone_change_notice_sends (notice_id);\n", "")],
 "editable": [(l + "\n", "") for l in [
     "CREATE TRIGGER phone_change_notices_append_only BEFORE UPDATE OR DELETE ON phone_change_notices\n  FOR EACH ROW EXECUTE FUNCTION phone_notices_append_only();",
     "CREATE TRIGGER phone_change_notice_sends_append_only BEFORE UPDATE OR DELETE ON phone_change_notice_sends\n  FOR EACH ROW EXECUTE FUNCTION phone_notices_append_only();"]],
 # Over-strict, for the control: no member's PIN is ever reset.
 "c_no_member_reset": [(SEC, "    IF true THEN")],
}
# M0: every rule of 0045 off. The partial mutants (a check narrowed to
# signatories) and the over-strict one are not in it; M0 turns those checks
# off entirely instead.
M0_OFF = ["reset_any_officer", "approval_silent", "phone_any_officer", "phone_absent_ok", "request_absent_ok",
          "notice_missing", "sent_twice", "editable"]
M0_EXTRA = [(RESET_TWO, OFF + "\n      PERFORM auth_refuse('signatory_reset_needs_two'"),
            (PHONE_TWO, OFF + "\n      PERFORM auth_refuse('signatory_phone_needs_two'")]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

m0 = apply(body(src), M0_OFF)
for old, new in M0_EXTRA:
    m0 = rep(m0, old, new, "M0")
hdr0 = ("-- 0045's INTERFACE: \"M0\" for suite 0045, GENERATED (gen0045.py) from the migration\n"
        "-- with every rule off: any officer resets anyone and changes any number, nobody\n"
        "-- says the member is present or why they approve, no notice to the old number,\n"
        "-- outcomes many and editable. Applied on top of the real 0001-0044.\n")
open(os.path.join(REPO, "test/tier1/0045_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + m0)

d = os.path.join(REPO, "test/tier1/mutants_0045")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0045.py): a full copy of 0045 with\n-- one rule broken, applied over 0045_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
