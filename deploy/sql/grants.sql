-- Applied by 40-migrate.sh after every `migrate up`, as argon_owner.
-- Explicit, not ALTER DEFAULT PRIVILEGES: a new table gets NO access for the
-- app until someone decides what access it should have and writes it here.
--
-- The ledger is append-only for the app at the privilege layer as well as the
-- trigger layer: SELECT and INSERT, never UPDATE, DELETE or TRUNCATE.
--
-- Server-generated facts (ledger_entries.posted_at, posting_xid;
-- signatory_approvals.signed_at) are left OUT of the INSERT column lists: the
-- app has no path to set them at all. The triggers refuse them too, for every
-- other role — the capability does not exist rather than being checked.

REVOKE ALL ON ALL TABLES IN SCHEMA public FROM argon_app;

-- Append-only.
GRANT SELECT ON
  ledger_batches, ledger_entries, ledger_lines, signatory_approvals, wallet_reconciliations
TO argon_app;
GRANT INSERT ON ledger_batches, ledger_lines, wallet_reconciliations TO argon_app;
GRANT INSERT (id, society_id, batch_id, kind, value_date, actor_user_id, actor_post,
              actor_post_label, reverses_id, adjusts_id, reason, source_line)
  ON ledger_entries TO argon_app;
GRANT INSERT (id, request_id, member_id, post_at_signing, user_id)
  ON signatory_approvals TO argon_app;

-- Mutable reference state. No DELETE: a society, member or user is ended by a
-- status change, not by vanishing. members.user_id is updatable (linking a
-- record to its person); relinking is checked by members_relink_auditor_check.
GRANT SELECT, INSERT, UPDATE ON societies, branches, members, accounts, wallets TO argon_app;
GRANT SELECT, INSERT ON users TO argon_app;
GRANT UPDATE (full_name) ON users TO argon_app;

-- LEDGER_CONTRACT.md §4.3: a handover is a NEW appointment. The only change an
-- existing appointment may undergo is being closed.
GRANT SELECT, INSERT ON appointments TO argon_app;
GRANT UPDATE (held_until) ON appointments TO argon_app;

-- An approval request moves through its states; its amount, kind and quorum
-- are fixed when raised.
GRANT SELECT, INSERT ON approval_requests TO argon_app;
GRANT UPDATE (state) ON approval_requests TO argon_app;

-- A period is opened, then closed; period_close_is_final refuses anything after.
GRANT SELECT, INSERT ON accounting_periods TO argon_app;
GRANT UPDATE (closed_at, closed_by) ON accounting_periods TO argon_app;

-- A reconciliation item is explained and resolved, which is an update of
-- those two columns only.
GRANT UPDATE (explanation, resolved_at) ON wallet_reconciliations TO argon_app;

-- ---- 0003 auth --------------------------------------------------------------
-- INSERT lists leave out every server-written column (times, act_xid,
-- privileged, attempts, consumed_at): the app has no path to set them. No
-- UPDATE or DELETE on credentials, proofs, codes or attempts: a PIN changes
-- by a credential_act, a proof is consumed and a code attempt counted only
-- inside the database's own functions.
GRANT SELECT ON auth_credentials, auth_proofs, auth_codes, auth_devices, auth_sessions,
                sign_in_attempts, credential_acts TO argon_app;
GRANT INSERT (user_id, pin_hash) ON auth_credentials TO argon_app;
GRANT INSERT (id, user_id, method) ON auth_proofs TO argon_app;
GRANT INSERT (id, user_id, purpose, code_digest, sent_to) ON auth_codes TO argon_app;
GRANT INSERT (id, user_id, token_digest) ON auth_devices TO argon_app;
GRANT UPDATE (revoked_at) ON auth_devices TO argon_app;
GRANT INSERT (id, user_id, device_id, token_digest) ON auth_sessions TO argon_app;
GRANT UPDATE (ended_at, ended_by_session) ON auth_sessions TO argon_app;
GRANT INSERT (user_id, credential_digest, device_id, succeeded) ON sign_in_attempts TO argon_app;
GRANT INSERT (id, kind, actor_user_id, actor_session_id, proof_id, target_user_id, target_member_id,
              new_phone, new_pin_hash, request_id, reason) ON credential_acts TO argon_app;

-- migrate's bookkeeping is not the app's business.
REVOKE ALL ON schema_migrations FROM argon_app;
