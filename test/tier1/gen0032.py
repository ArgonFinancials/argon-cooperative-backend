# Run from the repo root:  python3 test/tier1/gen0032.py .
# Generates test/tier1/0032_interface.sql (M0) and test/tier1/mutants_0032/*.sql
# from migrations/0032_contribution_claims.up.sql. Every replacement must apply
# exactly once, or the run stops.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0032_contribution_claims.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0032_contribution_claims.down.sql"), encoding="utf-8").read()

def body(t):  # strip the header comment block; keep BEGIN..COMMIT
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:70]!r}")
    return t.replace(old, new)

# ---- the rules ---------------------------------------------------------------
R = {
 "claim_anyone": [("  IF NOT EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND society_id = NEW.society_id AND user_id = NEW.user_id) THEN",
                   "  IF false THEN")],
 "session_any": [("CHECK (session_id ~ '^[0-9A-Za-z]{10,64}$')", "CHECK (true)")],
 "amount_any": [(",\n  CONSTRAINT claim_positive CHECK (amount_kobo > 0)\n);", "\n);")],
 "claim_twice": [("CREATE UNIQUE INDEX claim_once_per_member", "CREATE INDEX claim_once_per_member")],
 "no_match_on_inflow": [("    RETURN NULL;\n  END IF;\n  PERFORM contribution_match(NEW.society_id, NEW.session_id);", "    RETURN NULL;\n  END IF;")],
 "no_match_on_claim": [("BEGIN\n  PERFORM contribution_match(NEW.society_id, NEW.session_id);\n  RETURN NULL;", "BEGIN\n  RETURN NULL;")],
 "attributed": [("VALUES (i.society_id, 'contribution_in', d, NULL, NULL, 'Argon system, on VFD''s confirmation'",
                 "VALUES (i.society_id, 'contribution_in', d, NULL, NULL, 'Treasurer'")],
 "misdated": [("  d := (i.confirmed_at AT TIME ZONE 'Africa/Lagos')::date;", "  d := (i.confirmed_at AT TIME ZONE 'UTC')::date;")],
 "amount_loose": [("  IF c.amount_kobo <> i.amount_kobo THEN\n    RETURN;\n  END IF;\n", "")],
 "match_any_session": [("  SELECT count(DISTINCT member_id) INTO n FROM contribution_claims WHERE society_id = p_society AND session_id = p_session;",
                        "  SELECT count(DISTINCT member_id) INTO n FROM contribution_claims WHERE society_id = p_society;"),
                       ("  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society AND session_id = p_session;",
                        "  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society ORDER BY claimed_at LIMIT 1;")],
 "first_claim_wins": [("  IF n <> 1 THEN\n    RETURN;   -- nobody's claim yet, or two members': never a guess", "  IF n < 1 THEN\n    RETURN;"),
                      ("  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society AND session_id = p_session;",
                       "  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society AND session_id = p_session ORDER BY claimed_at LIMIT 1;")],
 "own_ignored": [("  IF m IS NOT NULL THEN\n    PERFORM contribution_post_inflow(NEW.id, m, NULL, 'own_account');", "  IF false THEN\n    PERFORM contribution_post_inflow(NEW.id, m, NULL, 'own_account');")],
 "own_any_society": [("   WHERE account_number = NEW.originator_account AND society_id = NEW.society_id AND member_id IS NOT NULL;",
                      "   WHERE account_number = NEW.originator_account AND member_id IS NOT NULL;")],
 "uncharged": [("    SELECT q.partner_fee_kobo, q.vat_kobo, q.statutory_levy_kobo, q.argon_margin_kobo INTO fee, vat, levy, margin\n      FROM charge_quote(i.society_id, 'contribution_in', i.amount_kobo, d) q;",
                "    fee := 0;")],
 "forge_ok": [("    IF NEW.kind <> 'contribution_in' OR NEW.vfd_outcome_id IS NOT NULL\n       OR NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = NEW.vfd_inflow_id) THEN",
               "    IF false THEN")],
 "unclaimed_all": [("    FROM vfd_inflows i\n   WHERE NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = i.id)\n", "    FROM vfd_inflows i\n")],
 "wait_unseen": [("    FROM contribution_claim_states s WHERE s.state = 'waiting' AND s.claimed_at < now() - claim_wait_window()",
                  "    FROM contribution_claim_states s WHERE false")],
 "contest_unseen": [("           WHEN EXISTS (SELECT 1 FROM contribution_claims o WHERE o.society_id = c.society_id AND o.session_id = c.session_id AND o.member_id <> c.member_id) THEN 'contested'\n", "")],
 "differs_unseen": [("    FROM contribution_claim_states s WHERE s.state = 'amount_differs'", "    FROM contribution_claim_states s WHERE false")],
 # Over-strict, for the control: every claim put in front of a person.
 "c_every_claim_seen": [("    FROM contribution_claim_states s WHERE s.state = 'waiting' AND s.claimed_at < now() - claim_wait_window()",
                         "    FROM contribution_claim_states s")],
 "claims_editable": [("CREATE TRIGGER contribution_claims_append_only BEFORE UPDATE OR DELETE ON contribution_claims FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();\n", "")],
}

# M0: every rule off at once; the posting machinery stays (it is behaviour,
# and the control must pass on it). Matching loosened to the session alone.
M0_OFF = ["claim_anyone", "session_any", "amount_any", "claim_twice", "amount_loose", "first_claim_wins",
          "own_any_society", "forge_ok", "wait_unseen", "contest_unseen", "differs_unseen", "claims_editable"]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            # first_claim_wins and match_any_session share a line; within M0 only first_claim_wins is used.
            t = rep(t, old, new, n)
    return t

m0 = apply(body(src), M0_OFF)
hdr0 = "-- 0032's INTERFACE: \"M0\" for suite 0032, GENERATED (gen0032.py) from the\n-- migration with every rule off: a claim by anyone, any session, any amount,\n-- twice; a match on the session alone, first claim wins, any amount; an own\n-- account in any society; an entry naming any inflow; no claim seen by a\n-- person; claims editable. The posting itself stays: it is what the control\n-- needs. Applied on top of the real 0001-0031.\n"
open(os.path.join(REPO, "test/tier1/0032_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + m0)

d = os.path.join(REPO, "test/tier1/mutants_0032")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    m = apply(body(src), [n])
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0032.py): a full copy of 0032 with\n-- one rule broken, applied over 0032_interface.sql, whose objects are removed first.\n" + prefix + "\n" + m)
print("interface and", len(R), "mutants")
