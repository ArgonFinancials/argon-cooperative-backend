# Run from the repo root:  python3 test/tier1/gen0034.py .
# Generates test/tier1/0034_interface.sql (M0) and test/tier1/mutants_0034/*.sql
# from migrations/0034_enrol_by_email.up.sql. Every replacement must apply
# exactly once, or the run stops.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0034_enrol_by_email.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0034_enrol_by_email.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

R = {
 "email_any_setter": [("  IF NOT EXISTS (SELECT 1 FROM appointments ap JOIN members m ON m.id = ap.member_id\n                  WHERE m.user_id = a.actor_user_id AND ap.society_id = NEW.society_id AND ap.post = 'secretary'",
                       "  IF false AND NOT EXISTS (SELECT 1 FROM appointments ap JOIN members m ON m.id = ap.member_id\n                  WHERE m.user_id = a.actor_user_id AND ap.society_id = NEW.society_id AND ap.post = 'secretary'")],
 "email_any_act": [("  IF NOT FOUND OR a.kind NOT IN ('member_email', 'member_register') OR a.act_xid IS DISTINCT FROM pg_current_xact_id()\n     OR (TG_OP = 'UPDATE' AND NEW.email_set_by_act IS NOT DISTINCT FROM OLD.email_set_by_act) THEN",
                    "  IF NOT FOUND THEN")],
 "email_any_case": [("    NEW.email := lower(btrim(NEW.email));", "    NEW.email := btrim(NEW.email);")],
 "email_shape_loose": [("CHECK (email IS NULL OR (length(email) <= 254 AND email = lower(email) AND email ~ '^[^@\\s,;<>]+@[^@\\s,;<>]+\\.[^@\\s,;<>.]+$'))",
                        "CHECK (email IS NULL OR email = lower(email))")],
 "email_record_rewritable": [("    IF (NEW.email_set_by_act, NEW.email_set_at) IS DISTINCT FROM (OLD.email_set_by_act, OLD.email_set_at) THEN", "    IF false THEN")],
 "email_set_at_unset": [("  NEW.email_set_at := auth_now();\n  RETURN NEW;", "  RETURN NEW;")],
 "slip_window_for_email": [("CASE NEW.delivery WHEN 'email' THEN enrolment_email_window() ELSE interval '30 days' END", "interval '30 days'")],
 "email_without_address": [("    IF v_email IS NULL THEN\n      PERFORM auth_refuse('enrolment_email_needs_address'", "    IF false THEN\n      PERFORM auth_refuse('enrolment_email_needs_address'")],
 "email_to_asked": [("    NEW.sent_to := v_email;\n  ELSE", "  ELSE")],
 "setter_may_issue": [("    IF member_email_setter(NEW.member_id) = a.actor_user_id THEN", "    IF false THEN")],
 "operator_may_email": [("    IF NEW.issued_by_act IS NULL THEN\n      PERFORM auth_refuse('enrolment_email_by_officer'", "    IF false THEN\n      PERFORM auth_refuse('enrolment_email_by_officer'")],
 "delivery_mutable": [("OLD.replaces_id, OLD.delivery, OLD.sent_to)", "OLD.replaces_id)"), ("NEW.replaces_id, NEW.delivery, NEW.sent_to)", "NEW.replaces_id)")],
 "setter_second_half": [("    IF NEW.delivery = 'email' AND member_email_setter(NEW.member_id) = a.actor_user_id THEN", "    IF false THEN")],
 "send_unsaid": [("CONSTRAINT email_send_says_what CHECK (CASE outcome WHEN 'sent' THEN code_id IS NOT NULL ELSE btrim(coalesce(detail, '')) <> '' END)",
                  "CONSTRAINT email_send_says_what CHECK (true)")],
 "send_editable": [("CREATE TRIGGER email_sends_append_only BEFORE UPDATE OR DELETE ON email_sends FOR EACH ROW EXECUTE FUNCTION email_send_append_only();\n", "")],
 # Over-strict, for the control: a slip held to the email's rules too.
 "c_every_code_emailed": [("  IF NEW.delivery = 'email' THEN\n    IF NEW.issued_by_act IS NULL THEN", "  IF true THEN\n    IF NEW.issued_by_act IS NULL THEN")],
}
M0_OFF = [k for k in R if not k.startswith("c_") and k not in ("email_any_act",)]
# email_any_act and email_any_setter both edit the act block; M0 takes the looser (act) and the setter.
M0_OFF.append("email_any_act")

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0034's INTERFACE: \"M0\" for suite 0034, GENERATED (gen0034.py) from the migration\n"
        "-- with every rule off: anyone sets an email, on any act, any shape and case, and\n"
        "-- rewrites who set it; an emailed code lives 30 days, needs no address, goes where\n"
        "-- it is told, from anyone, the operator included; delivery changes; a send says\n"
        "-- nothing and is edited. The email and the emailed code still exist: the control\n"
        "-- needs them. Applied on top of the real 0001-0033.\n")
open(os.path.join(REPO, "test/tier1/0034_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0034")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0034.py): a full copy of 0034 with\n-- one rule broken, applied over 0034_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
