# Run from the repo root:  python3 test/tier1/gen0033.py .
# Generates test/tier1/0033_interface.sql (M0) and test/tier1/mutants_0033/*.sql
# from migrations/0033_payment_resolutions.up.sql. Every replacement must apply
# exactly once, or the run stops.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0033_payment_resolutions.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0033_payment_resolutions.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

R = {
 "any_act": [("  IF NOT resolution_act_ok(NEW.proposed_act, NEW.proposed_by) THEN", "  IF false THEN")],
 "auditor_proposes": [("WHERE a.society_id = p_society AND m.user_id = p_user AND a.post <> 'internal_auditor'", "WHERE a.society_id = p_society AND m.user_id = p_user")],
 "anyone_proposes": [("  IF NOT resolution_officer(NEW.society_id, NEW.proposed_by) THEN", "  IF false THEN")],
 "no_reason": [("CONSTRAINT resolution_says_why CHECK (length(btrim(reason)) BETWEEN 10 AND 500)", "CONSTRAINT resolution_says_why CHECK (true)")],
 "reject_no_reason": [("CONSTRAINT resolution_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)", "CONSTRAINT resolution_says_why CHECK (true)")],
 "credit_any_society": [("OR NOT EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND society_id = NEW.society_id)", "OR NOT EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id)"),
                        ("OR NOT EXISTS (SELECT 1 FROM vfd_inflows WHERE id = NEW.inflow_id AND society_id = NEW.society_id)", "OR NOT EXISTS (SELECT 1 FROM vfd_inflows WHERE id = NEW.inflow_id)")],
 "credit_credited": [("    IF EXISTS (SELECT 1 FROM vfd_inflow_postings WHERE inflow_id = NEW.inflow_id) THEN\n      PERFORM auth_refuse('resolution_payment_uncredited', 'this payment is credited already');",
                      "    IF false THEN\n      PERFORM auth_refuse('resolution_payment_uncredited', 'this payment is credited already');")],
 "decline_closed": [("    IF NOT claim_open(NEW.claim_id) THEN", "    IF false THEN")],
 "two_open": [("CREATE UNIQUE INDEX resolution_open_once_per_payment", "CREATE INDEX resolution_open_once_per_payment")],
 "two_open_claim": [("CREATE UNIQUE INDEX resolution_open_once_per_claim", "CREATE INDEX resolution_open_once_per_claim")],
 "self_confirm": [("    IF NEW.decided_by = r.proposed_by THEN", "    IF false THEN")],
 "decided_twice": [("CREATE UNIQUE INDEX resolution_decided_once", "CREATE INDEX resolution_decided_once")],
 "beneficiary_ok": [("  IF resolution_beneficiary(NEW) = NEW.proposed_by THEN", "  IF false THEN")],
 "beneficiary_confirms": [("  IF resolution_beneficiary(r) = NEW.decided_by THEN", "  IF false THEN")],
 "not_posted": [("    PERFORM contribution_post_inflow(r.inflow_id, r.member_id, r.claim_id, 'officers');\n", "")],
 "officers_not_posted": [("           WHEN p.member_id = c.member_id AND (i.amount_kobo = c.amount_kobo OR p.how = 'officers') THEN 'posted'",
                          "           WHEN p.member_id = c.member_id AND i.amount_kobo = c.amount_kobo THEN 'posted'")],
 "decline_untold": [("           WHEN dr.reason IS NOT NULL THEN 'not_credited'\n", "")],
 "reject_posts": [("  IF NEW.confirmed AND r.decision = 'credit' THEN", "  IF r.decision = 'credit' THEN")],
 "reject_stays_open": [("  DELETE FROM payment_resolutions_open WHERE resolution_id = r.id;", "  DELETE FROM payment_resolutions_open WHERE resolution_id = r.id AND NEW.confirmed;")],
 "resolutions_editable": [("CREATE TRIGGER payment_resolutions_append_only BEFORE UPDATE OR DELETE ON payment_resolutions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();\n", "")],
 "decisions_editable": [("CREATE TRIGGER payment_resolution_decisions_append_only BEFORE UPDATE OR DELETE ON payment_resolution_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();\n", "")],
 "others_hang": [("           WHEN p.how = 'officers' AND p.member_id <> c.member_id THEN 'not_credited'\n", "")],
 "decline_after_close": [("    IF r.decision = 'decline' AND NOT claim_open(r.claim_id) THEN", "    IF false THEN")],
 "declined_matches": [("WHERE society_id = p_society AND session_id = p_session AND NOT claim_declined(id);\n  IF n <> 1", "WHERE society_id = p_society AND session_id = p_session;\n  IF n <> 1"),
                      ("  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society AND session_id = p_session AND NOT claim_declined(id);",
                       "  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society AND session_id = p_session;")],
 "key_twice": [("CREATE UNIQUE INDEX resolution_recorded_once", "CREATE INDEX resolution_recorded_once")],
 "awaiting_unseen": [("    FROM payment_resolutions r JOIN payment_resolutions_open o ON o.resolution_id = r.id\n", "    FROM payment_resolutions r JOIN payment_resolutions_open o ON o.resolution_id = r.id WHERE false\n")],
 # Over-strict, for the control: every proposal, decided or not, put in front of a person.
 "c_every_resolution_seen": [("    FROM payment_resolutions r JOIN payment_resolutions_open o ON o.resolution_id = r.id\n", "    FROM payment_resolutions r\n")],
}

M0_OFF = ["any_act", "auditor_proposes", "anyone_proposes", "no_reason", "reject_no_reason", "credit_any_society", "credit_credited",
          "decline_closed", "two_open", "two_open_claim", "self_confirm", "decided_twice", "beneficiary_ok", "beneficiary_confirms",
          "resolutions_editable", "decisions_editable", "decline_after_close", "key_twice", "awaiting_unseen"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0033's INTERFACE: \"M0\" for suite 0033, GENERATED (gen0033.py) from the\n"
        "-- migration with every rule off: any act, any person, no reason, any\n-- society, a credited payment, a closed claim, many open at once, the\n"
        "-- proposer confirming, decided twice, the beneficiary taking part, editable,\n-- unseen. The posting and what the member is told stay: the control needs\n"
        "-- them. Applied on top of the real 0001-0032.\n")
open(os.path.join(REPO, "test/tier1/0033_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0033")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0033.py): a full copy of 0033 with\n-- one rule broken, applied over 0033_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
