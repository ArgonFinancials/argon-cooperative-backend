-- Reverses 0002 ONLY on a database whose ledger is empty. Going back would
-- drop line-level member attribution, users, periods and adjustment links,
-- and would have to invent value_dates for unknown as-at dates — every one of
-- them a silent loss. Refuse instead.
BEGIN;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_entries) OR EXISTS (SELECT 1 FROM signatory_approvals)
     OR EXISTS (SELECT 1 FROM users) OR EXISTS (SELECT 1 FROM accounting_periods) THEN
    RAISE EXCEPTION '0002 down: refusing — ledger, signatures, users or periods hold data that 0001 cannot represent';
  END IF;
END $$;

DROP TRIGGER signatory_approvals_admissible ON signatory_approvals;
DROP FUNCTION signature_admissible();
ALTER TABLE signatory_approvals DROP CONSTRAINT signatory_approvals_one_per_user;
ALTER TABLE signatory_approvals ADD CONSTRAINT signatory_approvals_request_id_member_id_key UNIQUE (request_id, member_id);
ALTER TABLE signatory_approvals DROP COLUMN user_id;

DROP TRIGGER members_relink_auditor_check ON members;
DROP FUNCTION member_relink_auditor_check();
DROP TRIGGER appointments_auditor_holds_no_signing_post ON appointments;
DROP FUNCTION appointment_auditor_holds_no_signing_post();
DROP FUNCTION person_holds_overlapping(UUID, UUID, UUID, DATE, DATE, BOOLEAN, UUID);

DROP TRIGGER ledger_entries_c_not_in_closed_period ON ledger_entries;
DROP FUNCTION ledger_entry_not_in_closed_period();
DROP TABLE accounting_periods;
DROP FUNCTION period_close_is_final();

DROP TRIGGER ledger_entries_b_correction_same_society ON ledger_entries;
DROP FUNCTION ledger_correction_same_society();
ALTER TABLE ledger_entries DROP CONSTRAINT correction_is_reversal_or_adjustment,
                           DROP CONSTRAINT adjustment_states_reason,
                           DROP COLUMN adjusts_id;

ALTER TABLE ledger_batches ADD COLUMN as_of DATE;
ALTER TABLE ledger_entries DROP CONSTRAINT value_date_known_unless_opening_balance;
ALTER TABLE ledger_entries ALTER COLUMN value_date SET NOT NULL;
COMMENT ON COLUMN ledger_entries.value_date IS NULL;

DROP TRIGGER ledger_lines_admissible ON ledger_lines;
DROP FUNCTION ledger_line_admissible();
ALTER TABLE ledger_entries ADD COLUMN member_id UUID REFERENCES members(id);
CREATE INDEX ledger_entries_member_idx ON ledger_entries (member_id, posted_at DESC);
DROP INDEX ledger_lines_member_idx;
ALTER TABLE ledger_lines DROP COLUMN member_id;

DROP TRIGGER ledger_entries_a_server_facts ON ledger_entries;
DROP FUNCTION ledger_entry_server_facts();
ALTER TABLE ledger_entries DROP COLUMN posting_xid;

DROP FUNCTION society_today();
DROP FUNCTION lock_person(UUID, UUID);
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_actor_user_id_fkey;
DROP INDEX members_user_idx;
ALTER TABLE members DROP COLUMN user_id;
DROP TABLE users;

COMMIT;
