BEGIN;

DROP TABLE IF EXISTS signatory_approvals;
DROP TABLE IF EXISTS approval_requests;
DROP TABLE IF EXISTS wallet_reconciliations;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS ledger_lines;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS ledger_batches;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS appointments;
DROP TABLE IF EXISTS members;
DROP TABLE IF EXISTS branches;
DROP TABLE IF EXISTS societies;

DROP FUNCTION IF EXISTS assert_entry_balanced();
DROP FUNCTION IF EXISTS refuse_mutation();

DROP TYPE IF EXISTS approval_state;
DROP TYPE IF EXISTS movement_kind;
DROP TYPE IF EXISTS account_kind;
DROP TYPE IF EXISTS officer_post;
DROP TYPE IF EXISTS collection_channel;
DROP TYPE IF EXISTS member_status;
DROP TYPE IF EXISTS society_lifecycle;

COMMIT;
