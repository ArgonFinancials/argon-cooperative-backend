# Run from the repo root:  python3 test/tier1/gen0042.py .
# Generates test/tier1/0042_interface.sql (M0) and test/tier1/mutants_0042/*.sql
# from migrations/0042_disputes.up.sql.
import os, sys
REPO = sys.argv[1]
src = open(os.path.join(REPO, "migrations/0042_disputes.up.sql"), encoding="utf-8").read()
down = open(os.path.join(REPO, "migrations/0042_disputes.down.sql"), encoding="utf-8").read()

def body(t):
    return t[t.index("BEGIN;"):]

def rep(t, old, new, name):
    if t.count(old) != 1:
        sys.exit(f"{name}: {t.count(old)} matches for {old[:80]!r}")
    return t.replace(old, new)

OFF = "  IF false THEN"
R = {
 # The query.
 "by_anyone": [("  IF m.user_id IS DISTINCT FROM NEW.raised_by THEN", OFF)],
 "any_entry": [("  IF NOT EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id\n                  WHERE e.id = NEW.entry_id AND e.society_id = m.society_id AND l.member_id = m.id) THEN", OFF)],
 "no_question": [("  question   TEXT NOT NULL CONSTRAINT dispute_question_said CHECK (length(btrim(question)) BETWEEN 10 AND 500),", "  question   TEXT NOT NULL,")],
 "open_twice": [("  IF EXISTS (SELECT 1 FROM dispute_states s WHERE s.entry_id = NEW.entry_id AND s.member_id = NEW.member_id", "  IF false AND EXISTS (SELECT 1 FROM dispute_states s WHERE s.entry_id = NEW.entry_id AND s.member_id = NEW.member_id")],
 # The answer.
 "answer_anyone": [("  IF NOT dispute_officer(NEW.answered_by, d.society_id) THEN", OFF)],
 "officer_includes_auditor": [("WHERE om.user_id = p_user AND ap.society_id = p_society AND ap.post <> 'internal_auditor'", "WHERE om.user_id = p_user AND ap.society_id = p_society")],
 "no_answer_text": [("  answer      TEXT NOT NULL CONSTRAINT dispute_answer_said CHECK (length(btrim(answer)) BETWEEN 10 AND 1000),", "  answer      TEXT NOT NULL,")],
 "answer_any_time": [("  IF dispute_state(d.id) NOT IN ('open', 'reopened') THEN", OFF)],
 "answer_own": [("  IF NEW.answered_by = d.raised_by THEN", OFF)],
 # The reply.
 "reply_any_time": [("  IF dispute_state(d.id) <> 'answered' THEN", OFF)],
 "reply_anyone": [("  IF NEW.replied_by IS DISTINCT FROM d.raised_by THEN", OFF)],
 "reopen_silent": [(",\n  CONSTRAINT dispute_reopened_says_why CHECK (reply <> 'reopened' OR length(btrim(coalesce(note, ''))) BETWEEN 10 AND 500)", "")],
 # The correction.
 "same_officer_decides": [("  IF NEW.decided_by = c.proposed_by THEN", OFF)],
 "not_posted": [("    NEW.entry_id := dispute_post_reversal(c.id, NEW.decided_act, NEW.decided_by);\n", "")],
 "proposal_any_act": [("  IF NOT FOUND OR a.kind <> 'dispute_correction' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "proposal_anyone": [("  IF NOT dispute_officer(NEW.proposed_by, d.society_id) THEN", OFF)],
 "decision_any_act": [("  IF NOT FOUND OR a.kind <> 'dispute_correction_decision' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN", "  IF NOT FOUND THEN")],
 "decision_anyone": [("  IF NOT dispute_officer(NEW.decided_by, d.society_id) THEN", OFF)],
 "proposal_own": [("  IF NEW.proposed_by = d.raised_by THEN", OFF)],
 "decision_own": [("  IF NEW.decided_by = d.raised_by THEN", OFF)],
 "two_proposals": [("  IF dispute_state(d.id) IN ('accepted', 'corrected', 'correction_proposed') THEN", "  IF dispute_state(d.id) IN ('accepted', 'corrected') THEN")],
 "reject_silent": [(",\n  CONSTRAINT dispute_rejection_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)", "")],
 "decided_twice": [("CREATE UNIQUE INDEX dispute_correction_decided_once ON dispute_correction_decisions (correction_id);\n", "")],
 "rejection_unseen": [("              UNION ALL\n              SELECT k.at, k.id, 'reopened' FROM dispute_corrections c JOIN dispute_correction_decisions k ON k.correction_id = c.id\n               WHERE c.dispute_id = d.id AND NOT k.confirmed", "")],
 "correct_vfd": [("                  AND e.vfd_inflow_id IS NULL AND e.vfd_outcome_id IS NULL\n", "")],
 # Every reversal.
 "mirror_unchecked": [("  IF EXISTS (\n       (SELECT account_id, member_id, charge_role, -amount_kobo", "  IF false AND EXISTS (\n       (SELECT account_id, member_id, charge_role, -amount_kobo")],
 "reversed_twice": [("CREATE UNIQUE INDEX ledger_entry_reversed_once ON ledger_entries (reverses_id) WHERE reverses_id IS NOT NULL;\n", "")],
 "reverse_reversal": [("  IF EXISTS (SELECT 1 FROM ledger_entries WHERE id = NEW.reverses_id AND reverses_id IS NOT NULL) THEN", OFF)],
 # Nothing edited.
 "editable": [(l + "\n", "") for l in [
     "CREATE TRIGGER entry_disputes_append_only BEFORE UPDATE OR DELETE ON entry_disputes FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();",
     "CREATE TRIGGER dispute_answers_append_only BEFORE UPDATE OR DELETE ON dispute_answers FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();",
     "CREATE TRIGGER dispute_replies_append_only BEFORE UPDATE OR DELETE ON dispute_replies FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();",
     "CREATE TRIGGER dispute_corrections_append_only BEFORE UPDATE OR DELETE ON dispute_corrections FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();",
     "CREATE TRIGGER dispute_correction_decisions_append_only BEFORE UPDATE OR DELETE ON dispute_correction_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();"]],
 # Over-strict, for the control: nobody is an officer who may answer.
 "c_no_officers": [("  SELECT EXISTS (SELECT 1 FROM appointments ap JOIN members om ON om.id = ap.member_id\n                  WHERE om.user_id = p_user", "  SELECT false AND EXISTS (SELECT 1 FROM appointments ap JOIN members om ON om.id = ap.member_id\n                  WHERE om.user_id = p_user")],
}
# M0: every rule off. officer_includes_auditor and c_no_officers edit the
# officer function; M0 turns each caller's check off instead.
M0_OFF = [n for n in R if not n.startswith("c_") and n not in ("officer_includes_auditor", "rejection_unseen")]

def apply(t, names):
    for n in names:
        for old, new in R[n]:
            t = rep(t, old, new, n)
    return t

hdr0 = ("-- 0042's INTERFACE: \"M0\" for suite 0042, GENERATED (gen0042.py) from the migration\n"
        "-- with every rule off: anyone queries anything, answers, replies, proposes and\n"
        "-- decides, any number of times, on any act; nothing posted on confirmation; any\n"
        "-- reversal; everything editable. Applied on top of the real 0001-0041.\n")
open(os.path.join(REPO, "test/tier1/0042_interface.sql"), "w", encoding="utf-8", newline="\n").write(hdr0 + apply(body(src), M0_OFF))

d = os.path.join(REPO, "test/tier1/mutants_0042")
os.makedirs(d, exist_ok=True)
for f in os.listdir(d):
    os.remove(os.path.join(d, f))
prefix = "-- the interface's objects removed first (the down migration's body).\n" + body(down)
for n in R:
    fname = n if n.startswith("c_") else f"m_{n}"
    open(os.path.join(d, f"{fname}.sql"), "w", encoding="utf-8", newline="\n").write(
        f"-- DELIBERATELY WRONG ({fname}). GENERATED (gen0042.py): a full copy of 0042 with\n-- one rule broken, applied over 0042_interface.sql, whose objects are removed first.\n"
        + prefix + "\n" + apply(body(src), [n]))
print("interface and", len(R), "mutants")
