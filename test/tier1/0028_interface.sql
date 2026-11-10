-- 0028's INTERFACE: "M0" for suite 0028. The wallet's shape with NO RULES:
-- VFD accounts any shape, any number of times, for anyone; transfers to any
-- account for any amount under any reference; sends and requeries in any
-- order; outcomes on no evidence; nothing released or posted by the system;
-- the operator's function recording anything, unattributed. Applied on top of
-- the real 0001–0027.
BEGIN;

CREATE TABLE vfd_accounts (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id     UUID NOT NULL REFERENCES societies(id),
  member_id      UUID REFERENCES members(id),          -- null: the society's own account
  account_number TEXT NOT NULL,
  vfd_client_id  TEXT NOT NULL,
  vfd_account_id TEXT,
  account_name   TEXT,
  how            TEXT NOT NULL,                         -- opened_here | recorded_by_operator
  recorded_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE vfd_transfers (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  hold_id      UUID NOT NULL REFERENCES withdrawal_holds(id),
  reference    TEXT NOT NULL,
  from_account TEXT NOT NULL,
  to_account   TEXT NOT NULL,
  amount_kobo  BIGINT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- What VFD said, each time it was asked: the send (once), and every requery.
CREATE TABLE vfd_transfer_events (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_id UUID NOT NULL REFERENCES vfd_transfers(id),
  kind        TEXT NOT NULL,      -- sent | requeried
  result      TEXT NOT NULL,      -- sent: accepted|refused|not_reached|unknown; requeried: succeeded|not_found|pending|unknown
  code        TEXT,
  txn_status  TEXT,
  txn_id      TEXT,
  session_id  TEXT,
  amount_kobo BIGINT,
  vfd_time    TEXT,
  at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE vfd_transfer_outcomes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_id UUID NOT NULL REFERENCES vfd_transfers(id),
  result      TEXT NOT NULL,      -- paid | failed
  evidence_id UUID NOT NULL REFERENCES vfd_transfer_events(id),
  at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The system's own records: no user stands in for it (§13).
ALTER TABLE ledger_entries ALTER COLUMN actor_user_id DROP NOT NULL;
ALTER TABLE ledger_entries ADD COLUMN vfd_outcome_id UUID REFERENCES vfd_transfer_outcomes(id);
ALTER TABLE withdrawal_hold_releases ALTER COLUMN actor_user_id DROP NOT NULL;
ALTER TABLE withdrawal_hold_releases ADD COLUMN vfd_outcome_id UUID REFERENCES vfd_transfer_outcomes(id);

CREATE FUNCTION vfd_transfer_reference(p_hold UUID) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT 'Argon-' || replace(p_hold::text, '-', '')
$$;

CREATE FUNCTION operator_record_society_vfd_account(p_society UUID, p_number TEXT, p_client TEXT, p_account TEXT, p_name TEXT,
    p_operator TEXT, p_login TEXT, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE v UUID;
BEGIN
  INSERT INTO vfd_accounts (society_id, account_number, vfd_client_id, vfd_account_id, account_name, how)
    VALUES (p_society, coalesce(p_number, ''), coalesce(p_client, ''), p_account, p_name, 'recorded_by_operator') RETURNING id INTO v;
  RETURN v;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_record_society_vfd_account(UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;

-- The hold may rest on a signature's act: the SHAPE of the last approval
-- paying (any signature, any request: which one is 0028's rule).
CREATE OR REPLACE FUNCTION withdrawal_hold_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r        approval_requests%ROWTYPE;
  a        RECORD;
  q        RECORD;
  p        RECORD;
BEGIN
  IF NEW.act_id IS NULL THEN
    PERFORM auth_refuse('hold_needs_step_up', 'a payout is authorised on the authoriser''s own PIN step-up: the hold names its act');
  ELSE
    SELECT kind, actor_user_id, act_xid INTO a FROM credential_acts WHERE id = NEW.act_id;
    IF a.actor_user_id IS DISTINCT FROM NEW.actor_user_id THEN PERFORM auth_refuse('hold_act_is_holders', 'a hold rests on its authoriser''s own step-up, not someone else''s'); END IF;
    IF a.kind NOT IN ('withdrawal_authorisation', 'approval_signature') THEN PERFORM auth_refuse('hold_act_kind', format('a hold rests on a withdrawal_authorisation act or a signature, not %s', a.kind)); END IF;
    IF a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN PERFORM auth_refuse('hold_act_same_transaction', 'the act and the hold are one event: the act is written in the hold''s transaction'); END IF;
  END IF;
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id FOR UPDATE;   -- one authorisation at a time per request
  IF r.kind IS DISTINCT FROM 'withdrawal_out' OR approval_status(r.id) <> 'approved' THEN
    PERFORM auth_refuse('hold_request_approved', format('request %s is %s, not an approved withdrawal', r.id, approval_status(r.id)));
  END IF;
  IF NEW.amount_kobo <> r.amount_kobo OR NEW.member_id IS DISTINCT FROM r.member_id OR NEW.society_id <> r.society_id THEN
    PERFORM auth_refuse('hold_matches_request', format('a hold is for its request: %s kobo for member %s, not %s for %s', r.amount_kobo, r.member_id, NEW.amount_kobo, NEW.member_id));
  END IF;
  IF EXISTS (SELECT 1 FROM withdrawal_holds h WHERE h.request_id = r.id AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)) THEN
    PERFORM auth_refuse('hold_once_open', 'this request already has an open hold: release it before authorising the payout again');
  END IF;
  SELECT * INTO q FROM charge_quote(NEW.society_id, 'withdrawal_out', NEW.amount_kobo, current_date);
  NEW.partner_fee_kobo := q.partner_fee_kobo;
  NEW.vat_kobo := q.vat_kobo;
  NEW.statutory_levy_kobo := q.statutory_levy_kobo;
  NEW.argon_margin_kobo := q.argon_margin_kobo;
  PERFORM 1 FROM members WHERE id = NEW.member_id FOR UPDATE;   -- one authorisation at a time per member
  SELECT * INTO p FROM member_position(NEW.member_id);              -- the one arithmetic (0018)
  IF p.floor_kobo IS NULL THEN
    PERFORM auth_refuse('account_floor_unset', 'this society has not set its account floor');
  END IF;
  IF NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo > p.withdrawable_kobo THEN
    PERFORM auth_refuse('withdrawal_within_withdrawable', format('%s kobo out (the amount and its charges) against %s withdrawable: savings %s, held for guarantees %s, floor %s, already authorised %s', NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo, p.withdrawable_kobo, p.savings_kobo, p.held_kobo, p.floor_kobo, p.open_holds_kobo));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

COMMIT;
