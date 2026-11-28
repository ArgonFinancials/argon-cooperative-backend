-- Applied by 40-migrate.sh after every `migrate up`, as argon_owner.
-- Explicit, not ALTER DEFAULT PRIVILEGES: a new table gets NO access for the
-- app until someone decides what access it should have and writes it here.
--
-- The ledger is append-only for the app at the privilege layer as well as the
-- trigger layer: SELECT and INSERT, never UPDATE, DELETE or TRUNCATE.
--
-- Server-generated facts (ledger_entries.posted_at, posting_xid;
-- signatory_approvals.signed_at, approval_rejections.rejected_at) are left OUT
-- of the INSERT column lists: the
-- app has no path to set them at all. The triggers refuse them too, for every
-- other role — the capability does not exist rather than being checked.

REVOKE ALL ON ALL TABLES IN SCHEMA public FROM argon_app;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM argon_auth;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'argon_wallet') THEN
    REVOKE ALL ON ALL TABLES IN SCHEMA public FROM argon_wallet;
  END IF;
END $$;

-- Append-only.
GRANT SELECT ON
  ledger_batches, ledger_entries, ledger_lines, signatory_approvals
TO argon_app;
GRANT INSERT ON ledger_batches, ledger_lines TO argon_app;
-- act_id, request_id, payout_reference (0011): a single movement's step-up act,
-- the approved request a payout pays, and the transfer that paid it.
-- client_reference (0015): the client's reference for a single contribution,
-- which 0015 requires; without it here the app could record none (finding 25).
GRANT INSERT (id, society_id, batch_id, kind, value_date, actor_user_id, actor_post,
              actor_post_label, reverses_id, adjusts_id, reason, source_line, loan_id,
              act_id, request_id, payout_reference, hold_id, client_reference)
  ON ledger_entries TO argon_app;

-- The payout hold (0012). The app authorises a payout (a hold) and releases
-- one, but never writes a hold's charges (the server computes them) and never
-- writes a flag (only the rules do, at commit). Both are append-only.
GRANT SELECT ON withdrawal_holds, withdrawal_hold_releases, ledger_discrepancies TO argon_app;
GRANT INSERT (id, request_id, society_id, member_id, amount_kobo, actor_user_id, act_id) ON withdrawal_holds TO argon_app;
GRANT INSERT (hold_id, actor_user_id, reason) ON withdrawal_hold_releases TO argon_app;
-- act_id (0010): the signer's own step-up act, written by the app in the
-- signature's transaction. Without it in this list the app cannot sign at all
-- (finding 25), however right the rule.
GRANT INSERT (id, request_id, member_id, post_at_signing, user_id, act_id)
  ON signatory_approvals TO argon_app;

-- Mutable reference state. No DELETE: a society, member or user is ended by a
-- status change, not by vanishing. members.user_id is updatable (linking a
-- record to its person); relinking is checked by members_relink_auditor_check.
GRANT SELECT, INSERT, UPDATE ON branches, members, accounts TO argon_app;

-- A society: its descriptive fields only (0011, LEDGER_CONTRACT §10). Its
-- quorums (signatures_required, write_off_signatures_required,
-- withdrawal_signatures_required), account floor, guard threshold and
-- lifecycle are bye-law and platform decisions: set by the operator script
-- until a request kind exists to change them. Neither INSERT nor UPDATE: an
-- app that could INSERT them could create a society with a quorum of 1, and
-- taking away only UPDATE would look like the hole was closed.
GRANT SELECT ON societies TO argon_app;
GRANT INSERT (id, code, registered_name, registration_number, state_directorate) ON societies TO argon_app;
GRANT UPDATE (registered_name, state_directorate) ON societies TO argon_app;

-- Charges (0011) are configuration the operator writes as the owner. The app
-- reads them; it never writes a figure it will later be charged by.
GRANT SELECT ON charge_schedules, charge_bands TO argon_app;
GRANT SELECT, INSERT ON users TO argon_app;
GRANT UPDATE (full_name) ON users TO argon_app;

-- LEDGER_CONTRACT.md §4.3: a handover is a NEW appointment. The only change an
-- existing appointment may undergo is being closed.
GRANT SELECT, INSERT ON appointments TO argon_app;
GRANT UPDATE (held_until) ON appointments TO argon_app;

-- An approval request is raised and never updated: its status is DERIVED
-- (approval_status, 0007) from signatures, rejections and expiry, so there is
-- no status to write. (Until 0007 the app held UPDATE (state): finding 14.)
GRANT SELECT, INSERT ON approval_requests TO argon_app;

-- A rejection is an append-only, attributable row; rejected_at is the
-- server's, left out of the INSERT list as signed_at is.
GRANT SELECT ON approval_rejections TO argon_app;
GRANT INSERT (id, request_id, member_id, user_id, post_at_rejection, reason, note, act_id)
  ON approval_rejections TO argon_app;

-- Loans (0008). A product's terms are configuration the society may change
-- (agreed loans keep their snapshot); the rate is not in the list: it is
-- refused above zero until a method is agreed, and opened when it is. An
-- agreement, its schedule and every guarantee are append-only, and their
-- server facts (agreed_at, agreed_xid, at) are not in the INSERT lists.
GRANT SELECT, INSERT ON loan_products TO argon_app;
GRANT UPDATE (name, max_term_months, guarantors_required, savings_multiple_pct, allows_prepayment)
  ON loan_products TO argon_app;
GRANT SELECT ON loans, loan_instalments, loan_guarantees, loan_guarantee_withdrawals TO argon_app;
GRANT INSERT (id, society_id, member_id, product_id, principal_kobo, term_months, purpose, guarantors_required,
              savings_multiple_pct, interest_rate_bp, allows_prepayment, supersedes_id) ON loans TO argon_app;
GRANT INSERT ON loan_instalments TO argon_app;
GRANT INSERT (id, loan_id, guarantor_member_id, guarantor_user_id, act_id) ON loan_guarantees TO argon_app;
GRANT INSERT (guarantee_id, act_id) ON loan_guarantee_withdrawals TO argon_app;

-- A period is opened, then closed; period_close_is_final refuses anything after.
GRANT SELECT, INSERT ON accounting_periods TO argon_app;
GRANT UPDATE (closed_at, closed_by) ON accounting_periods TO argon_app;

-- 0001's wallets and wallet_reconciliations, until 0037 drops them (nothing
-- ever used them). A reconciliation item was explained and resolved, an
-- update of those two columns only.
DO $$
BEGIN
  IF to_regclass('public.wallet_reconciliations') IS NOT NULL THEN
    GRANT SELECT, INSERT ON wallet_reconciliations TO argon_app;
    GRANT UPDATE (explanation, resolved_at) ON wallet_reconciliations TO argon_app;
    GRANT SELECT, INSERT, UPDATE ON wallets TO argon_app;
  END IF;
END $$;

-- ---- 0003 auth, split by role (0005) -----------------------------------------
-- INSERT lists leave out every server-written column (times, act_xid,
-- mint_xid, privileged, attempts, consumed_at): no role has a path to set
-- them. No UPDATE or DELETE on credentials, proofs, codes or attempts: a PIN
-- changes by a credential_act, a proof is consumed and a code attempt counted
-- only inside the database's own functions.
--
-- WHO WRITES AUTH STATE. The PIN is checked in Go, not in the database, so
-- whoever can INSERT a PIN proof can skip the PIN, and whoever can INSERT a
-- sign-in success can end a lockout run. Only argon_auth (the auth service)
-- may. argon_app (the API's handlers) READS auth state — session_valid runs
-- with the caller's privileges — and writes only the attributable act that
-- spends a proof. argon_auth writes no act and no ledger row. The probe
-- proves both halves (P11–P14), so the split is shown, not just configured.
--
-- And what argon_app READS: only what session_valid needs (sessions and
-- devices, which hold token digests) and the acts it writes, WITHOUT their PIN
-- hash. Never PIN hashes (auth_credentials, credential_acts.new_pin_hash),
-- code digests or attempts: a handler role that can read PIN hashes is an
-- offline attack on every PIN in the system (probe P15–P17).
GRANT SELECT ON auth_devices, auth_sessions TO argon_app;
GRANT SELECT (id, kind, actor_user_id, actor_session_id, proof_id, target_user_id, target_member_id,
              new_phone, request_id, reason, at, act_xid) ON credential_acts TO argon_app;
GRANT INSERT (id, kind, actor_user_id, actor_session_id, proof_id, target_user_id, target_member_id,
              new_phone, new_pin_hash, request_id, reason) ON credential_acts TO argon_app;

GRANT SELECT ON users, members, appointments, auth_credentials, auth_proofs, auth_codes, auth_devices,
                auth_sessions, sign_in_attempts, credential_acts TO argon_auth;
GRANT INSERT (user_id, pin_hash) ON auth_credentials TO argon_auth;
GRANT INSERT (id, user_id, purpose, code_digest, sent_to) ON auth_codes TO argon_auth;
GRANT INSERT (id, user_id, token_digest) ON auth_devices TO argon_auth;
GRANT UPDATE (revoked_at) ON auth_devices TO argon_auth;
GRANT INSERT (id, user_id, device_id, token_digest) ON auth_sessions TO argon_auth;
GRANT UPDATE (ended_at, ended_by_session) ON auth_sessions TO argon_auth;
GRANT INSERT (user_id, credential_digest, device_id, succeeded) ON sign_in_attempts TO argon_auth;
-- 0019: the record of each code handed to the SMS provider. The sign-in
-- service writes and reads it; the app neither (probes P49, P50).
DO $$
BEGIN
  IF to_regclass('public.sms_sends') IS NOT NULL THEN
    GRANT SELECT, INSERT (code_id, provider, outcome, provider_message_id, provider_code) ON sms_sends TO argon_auth;
  END IF;
END $$;
-- A proof names the act it is minted for once 0005 has added the column.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
              WHERE table_schema = 'public' AND table_name = 'auth_proofs' AND column_name = 'act_kind') THEN
    GRANT INSERT (id, user_id, method, act_kind) ON auth_proofs TO argon_auth;
  ELSE
    GRANT INSERT (id, user_id, method) ON auth_proofs TO argon_auth;
  END IF;
END $$;

-- 0024: enrolment codes. The app issues an officer's code (the step-up act
-- and the code, one transaction) and reads a code's history: never its
-- digest, and never the operator's columns, so the operator's path is the
-- owner's function alone. The auth service redeems through enrolment_redeem
-- only; no role updates a code directly (probes P52–P55).
DO $$
BEGIN
  IF to_regclass('public.enrolment_codes') IS NOT NULL THEN
    GRANT INSERT (id, user_id, member_id, code_digest, issued_by_act) ON enrolment_codes TO argon_app;
    GRANT SELECT (id, user_id, member_id, issued_at, expires_at, issued_by_act, issued_by_operator, replaces_id,
                  wrong_tries, used_at, voided_at, void_reason) ON enrolment_codes TO argon_app;
    -- 0025: the redemption trusts and names the device; 0024's (no device) is
    -- dropped there. An officer sees from what device a slip was redeemed.
    IF to_regprocedure('enrolment_redeem(uuid,text,text)') IS NOT NULL THEN
      GRANT SELECT (used_device_id) ON enrolment_codes TO argon_app;
      GRANT EXECUTE ON FUNCTION enrolment_redeem(UUID, TEXT, TEXT) TO argon_auth;
    ELSE
      GRANT EXECUTE ON FUNCTION enrolment_redeem(UUID, TEXT) TO argon_auth;
    END IF;
    -- 0026: a signatory's code in two halves. The app writes half one with
    -- the first act, and half two with the second; never a code's digest
    -- (the server assembles it), and it reads no half (probes P57, P58).
    -- 0030: the auth service asks whether a code is the person's own spent
    -- slip, to tell the member whose slip was taken (§12). Only it (P69).
    IF to_regprocedure('enrolment_slip_used(uuid,text)') IS NOT NULL THEN
      GRANT EXECUTE ON FUNCTION enrolment_slip_used(UUID, TEXT) TO argon_auth;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'enrolment_codes' AND column_name = 'first_half_digest') THEN
      GRANT INSERT (first_half_digest) ON enrolment_codes TO argon_app;
      GRANT UPDATE (second_half_digest, second_by_act) ON enrolment_codes TO argon_app;
      GRANT SELECT (second_by_act, pending) ON enrolment_codes TO argon_app;
    END IF;
  END IF;
END $$;

-- 0028: THE WALLET. Only argon_wallet (the wallet worker) writes what VFD
-- said: accounts, transfers, VFD's answers and their outcomes. The database
-- cannot know what VFD said, so whoever can write "paid" can debit a member
-- for money that never left, and an account number decides where money goes.
-- Never the server's times (recorded_at, created_at, at): no role sets them.
-- The app READS the wallet (a request's payment, the exceptions), and its
-- entry and release INSERT lists above do not carry vfd_outcome_id: the
-- system's records are written only by the outcome's own trigger (P60–P62).
DO $$
BEGIN
  IF to_regclass('public.vfd_accounts') IS NOT NULL THEN
    GRANT SELECT ON vfd_accounts, vfd_transfers, vfd_transfer_events, vfd_transfer_outcomes TO argon_app;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'argon_wallet') THEN
      GRANT SELECT ON vfd_accounts, vfd_transfers, vfd_transfer_events, vfd_transfer_outcomes,
                      withdrawal_holds, withdrawal_hold_releases TO argon_wallet;
      GRANT SELECT (id, kind) ON credential_acts TO argon_wallet;
      GRANT SELECT (id, hold_id) ON ledger_entries TO argon_wallet;
      GRANT INSERT (id, society_id, member_id, account_number, vfd_client_id, vfd_account_id, account_name, how) ON vfd_accounts TO argon_wallet;
      GRANT INSERT (id, hold_id, reference, from_account, to_account, amount_kobo) ON vfd_transfers TO argon_wallet;
      GRANT INSERT (id, transfer_id, kind, result, code, txn_status, txn_id, session_id, amount_kobo, vfd_time) ON vfd_transfer_events TO argon_wallet;
      GRANT INSERT (id, transfer_id, result, evidence_id) ON vfd_transfer_outcomes TO argon_wallet;
    END IF;
  END IF;
END $$;

-- 0029: MONEY ARRIVING. The webhook (the app) KEEPS a notification, the
-- columns VFD sent only, never its received time; it writes no attempt, no
-- inflow and no reconciliation (P66-P68). The wallet confirms: it reads the
-- notifications and writes the attempts, the inflows (VFD's word) and the
-- reconciliation runs, never their server times.
DO $$
BEGIN
  IF to_regclass('public.vfd_notifications') IS NOT NULL THEN
    GRANT SELECT ON vfd_notifications, vfd_notification_attempts, vfd_inflows, vfd_reconciliation_runs, wallet_exceptions TO argon_app;
    GRANT INSERT (id, kind, raw, signed, reference, account_number) ON vfd_notifications TO argon_app;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'argon_wallet') THEN
      GRANT SELECT ON vfd_notifications, vfd_notification_attempts, vfd_inflows, vfd_reconciliation_runs TO argon_wallet;
      GRANT INSERT (id, notification_id, result, detail) ON vfd_notification_attempts TO argon_wallet;
      GRANT INSERT (id, vfd_reference, society_id, account_number, amount_kobo, session_id, originator_name, originator_account,
                    originator_bank, narration, vfd_time, notification_id) ON vfd_inflows TO argon_wallet;
      GRANT INSERT (id, account_number, period_from, period_to, outcome, detail) ON vfd_reconciliation_runs TO argon_wallet;
    END IF;
  END IF;
END $$;

-- 0031: THE REGISTER. register_member is the ONE way the app adds a member:
-- it rests on the society's secretary's own act and checks the record. The
-- app's INSERT on members (above) is taken back once it exists (P70); UPDATE
-- (relinking) stays. A register batch is recorded by the app, never its time.
DO $$
BEGIN
  IF to_regprocedure('register_member(uuid,text,text,text,text,date,collection_channel,text,text,text,uuid,uuid,integer)') IS NOT NULL THEN
    REVOKE INSERT ON members FROM argon_app;
    GRANT EXECUTE ON FUNCTION register_member(UUID, TEXT, TEXT, TEXT, TEXT, DATE, collection_channel, TEXT, TEXT, TEXT, UUID, UUID, INT) TO argon_app;
    GRANT SELECT ON member_register_batches TO argon_app;
    -- Whether a member HAS a PIN (the register's hasPin), never the hash
    -- (P71): user_id only.
    GRANT SELECT (user_id) ON auth_credentials TO argon_app;
    GRANT INSERT (id, society_id, reference, act_id, row_count) ON member_register_batches TO argon_app;
  END IF;
END $$;

-- 0032: A CONTRIBUTION CLAIMED. The app records a member's claim (its columns,
-- never its time) and reads each claim's state; it writes no posting and no
-- entry naming an inflow (its ledger_entries INSERT list above does not carry
-- vfd_inflow_id), and cannot call the posting or the match (P72–P75). The
-- database posts, in the claim's or the inflow's own transaction. The wallet
-- runs the rematch on its tick, for what a race missed; it calls nothing else
-- (P76).
DO $$
BEGIN
  IF to_regclass('public.contribution_claims') IS NOT NULL THEN
    GRANT SELECT ON contribution_claims, contribution_claim_states TO argon_app;
    GRANT INSERT (id, society_id, member_id, user_id, session_id, amount_kobo) ON contribution_claims TO argon_app;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'argon_wallet') THEN
      GRANT EXECUTE ON FUNCTION contribution_rematch() TO argon_wallet;
    END IF;
  END IF;
END $$;

-- 0033: A PAYMENT RESOLVED BY TWO OFFICERS. The app records an officer's
-- proposal and the second officer's decision (their columns, never their
-- times); the database checks both and posts the credit. The app cannot write
-- what is open (P77), backdate a proposal (P78), or change a decision (P79).
-- It reads which payments are credited (the unmatched list).
DO $$
BEGIN
  IF to_regclass('public.payment_resolutions') IS NOT NULL THEN
    GRANT SELECT ON payment_resolutions, payment_resolution_decisions, payment_resolutions_open, vfd_inflow_postings TO argon_app;
    GRANT INSERT (id, society_id, decision, inflow_id, claim_id, member_id, reason, proposed_by, proposed_act, client_reference) ON payment_resolutions TO argon_app;
    GRANT INSERT (id, resolution_id, confirmed, decided_by, decided_act, reason) ON payment_resolution_decisions TO argon_app;
  END IF;
END $$;

-- 0034: OFFICERS ENROL BY EMAIL. The app sets a member's email with the
-- secretary's act (the trigger checks the act; the time is the database's),
-- and relinks a record to its person; it updates nothing else on members
-- (P80). It asks for an emailed code (delivery) but never names the address
-- it goes to: the database writes the member's own (P81). It records each
-- send, never its time (P82), and never edits one (P83).
DO $$
BEGIN
  IF to_regclass('public.email_sends') IS NOT NULL THEN
    REVOKE UPDATE ON members FROM argon_app;
    GRANT UPDATE (user_id, email, email_set_by_act) ON members TO argon_app;
    GRANT INSERT (delivery) ON enrolment_codes TO argon_app;
    GRANT SELECT (delivery, sent_to) ON enrolment_codes TO argon_app;
    GRANT SELECT ON email_sends TO argon_app;
    GRANT INSERT (id, member_id, code_id, kind, sent_to, outcome, provider_id, detail) ON email_sends TO argon_app;
  END IF;
END $$;

-- 0035: DEMONSTRATION SOCIETIES. The auth service asks whether a person is a
-- demonstration's and nothing else's (K68), to trust a device on the PIN
-- alone. The app neither marks a society (P85) nor sets a PIN by the
-- operator's path (P84): those are the owner's functions, the operator's.
DO $$
BEGIN
  IF to_regprocedure('public.auth_demo_only(uuid)') IS NOT NULL THEN
    GRANT EXECUTE ON FUNCTION auth_demo_only(uuid) TO argon_auth;
  END IF;
END $$;

-- 0039: VFD AS PRODUCTION PROVED IT. The webhook says a notification came
-- from VFD's webhook; the wallet worker records Argon's pool once, from VFD's
-- enquiry, keeps every reply VFD gives a send or a requery, and keeps a
-- deposit found in an account's history as a notification from the history.
DO $$
BEGIN
  IF to_regclass('public.vfd_pool') IS NOT NULL THEN
    GRANT INSERT (source) ON vfd_notifications TO argon_app;
    GRANT SELECT ON vfd_pool TO argon_app;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'argon_wallet') THEN
      GRANT SELECT ON vfd_pool TO argon_wallet;
      GRANT INSERT (id, account_number, account_id, client_id, client_name) ON vfd_pool TO argon_wallet;
      GRANT INSERT (http_status, reply) ON vfd_transfer_events TO argon_wallet;
      GRANT INSERT (id, kind, raw, signed, reference, account_number, source) ON vfd_notifications TO argon_wallet;
    END IF;
  END IF;
END $$;

-- 0038: A MEMBER'S STATUS. The app records a change (the member, the status
-- it goes to, the reason, the secretary's act); the database says where it
-- came from, when, and for which society (P86), and moves the member: the app
-- never sets a status itself (P87).
DO $$
BEGIN
  IF to_regclass('public.member_status_changes') IS NOT NULL THEN
    GRANT SELECT ON member_status_changes TO argon_app;
    GRANT INSERT (id, member_id, to_status, reason, act_id) ON member_status_changes TO argon_app;
  END IF;
END $$;

-- 0040: A MEMBER'S OWN BANK ACCOUNT. The app records a member's details
-- (the member, the number, the bank, the setter's act); the database says who
-- set them, whether it was the member, and when (P89). The app never records
-- what VFD said about them (P88): only the wallet, the only holder of VFD's
-- token, does, and it never says whether the name matches (P92) or records
-- details itself (P91). A confirmation names its act and its reason; who
-- confirmed is the act's (P93). Nothing is rewritten (P90).
DO $$
BEGIN
  IF to_regclass('public.member_bank_accounts') IS NOT NULL THEN
    GRANT SELECT ON member_bank_accounts, bank_name_checks, bank_name_confirmations, member_bank_current TO argon_app;
    GRANT INSERT (id, member_id, account_number, bank_code, set_by_act) ON member_bank_accounts TO argon_app;
    GRANT INSERT (id, bank_account_id, act_id, reason) ON bank_name_confirmations TO argon_app;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'argon_wallet') THEN
      GRANT SELECT ON member_bank_current, member_bank_accounts, bank_name_checks, bank_name_confirmations TO argon_wallet;
      GRANT INSERT (id, bank_account_id, found, vfd_code, vfd_name) ON bank_name_checks TO argon_wallet;
      GRANT INSERT (to_bank) ON vfd_transfers TO argon_wallet;
    END IF;
  END IF;
END $$;

-- 0042: DISPUTES. The app records a member's query, an officer's answer, the
-- member's reply, a correction's proposal and its decision (who and the words;
-- every time is the database's). It never posts a correction itself (P94):
-- the database posts the reversal on a second officer's confirmation, and
-- names it (P95). Nothing is rewritten (P96).
DO $$
BEGIN
  IF to_regclass('public.entry_disputes') IS NOT NULL THEN
    GRANT SELECT ON entry_disputes, dispute_answers, dispute_replies, dispute_corrections, dispute_correction_decisions, dispute_states TO argon_app;
    GRANT INSERT (id, entry_id, member_id, raised_by, question) ON entry_disputes TO argon_app;
    GRANT INSERT (id, dispute_id, answered_by, answer) ON dispute_answers TO argon_app;
    GRANT INSERT (id, dispute_id, replied_by, reply, note) ON dispute_replies TO argon_app;
    GRANT INSERT (id, dispute_id, proposed_by, proposed_act, reason) ON dispute_corrections TO argon_app;
    GRANT INSERT (id, correction_id, confirmed, decided_by, decided_act, reason) ON dispute_correction_decisions TO argon_app;
  END IF;
END $$;

-- 0043: REVERSALS AND ADJUSTMENTS. The app records a proposal (the entry, the
-- kind, the lines, the reason, the proposer's act) and a decision; the
-- database posts a confirmed one itself (P97) and names what it posted (P98).
-- Nothing is rewritten (P99).
DO $$
BEGIN
  IF to_regclass('public.entry_corrections') IS NOT NULL THEN
    GRANT SELECT ON entry_corrections, entry_correction_decisions TO argon_app;
    GRANT INSERT (id, entry_id, kind, lines, reason, proposed_by, proposed_act) ON entry_corrections TO argon_app;
    GRANT INSERT (id, correction_id, confirmed, decided_by, decided_act, reason) ON entry_correction_decisions TO argon_app;
  END IF;
END $$;

-- 0044: PERIOD CLOSE. The app records a close's proposal (the society, the
-- date, the proposer's act) and its decision; the database checks the trial
-- balance and keeps it (P101). A period is no longer closed by one person
-- through 0002's accounting_periods (P100). Nothing is rewritten (P102).
DO $$
BEGIN
  IF to_regclass('public.period_closes') IS NOT NULL THEN
    REVOKE UPDATE ON accounting_periods FROM argon_app;
    GRANT SELECT ON period_closes, period_close_decisions TO argon_app;
    GRANT INSERT (id, society_id, through, proposed_by, proposed_act) ON period_closes TO argon_app;
    GRANT INSERT (id, close_id, confirmed, decided_by, decided_act, reason) ON period_close_decisions TO argon_app;
  END IF;
END $$;

-- migrate's bookkeeping is not the app's business.
REVOKE ALL ON schema_migrations FROM argon_app;
