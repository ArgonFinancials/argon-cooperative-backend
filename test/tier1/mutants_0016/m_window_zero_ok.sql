-- DELIBERATELY WRONG. A window of zero days is accepted.
--
-- Must turn red: TestRaise10_TheWindowIsAtLeastOneDay.
BEGIN;
CREATE FUNCTION withdrawal_request_authored() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a RECORD;
BEGIN
  IF NEW.kind <> 'withdrawal_out' THEN
    RETURN NEW;
  END IF;
  IF NEW.raised_by_user_id IS NULL THEN
    PERFORM auth_refuse('withdrawal_request_names_raiser', 'a withdrawal request names who raised it: a request that moves money has an author');
  END IF;
  IF NEW.act_id IS NULL THEN
    PERFORM auth_refuse('withdrawal_request_needs_step_up', 'a withdrawal request is raised on its raisers own PIN step-up: it names its act');
  END IF;
  SELECT kind, actor_user_id, act_xid INTO a FROM credential_acts WHERE id = NEW.act_id;
  -- A missing raiser is the raiser rule's alone: compared only when one is named.
  IF NEW.raised_by_user_id IS NOT NULL AND a.actor_user_id IS DISTINCT FROM NEW.raised_by_user_id THEN
    PERFORM auth_refuse('withdrawal_request_act_is_raisers', 'a request rests on its raisers own step-up, not someone elses');
  END IF;
  IF a.kind IS DISTINCT FROM 'withdrawal_request' THEN
    PERFORM auth_refuse('withdrawal_request_act_kind', format('a withdrawal request rests on a withdrawal_request act, not %s', a.kind));
  END IF;
  IF a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('withdrawal_request_act_same_transaction', 'the act and the request are one event: the act is written in the requests transaction');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_withdrawal_r1_authored BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION withdrawal_request_authored();
CREATE FUNCTION withdrawal_request_raiser_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.kind <> 'withdrawal_out' OR NEW.raised_by_user_id IS NULL THEN
    RETURN NEW;
  END IF;
  IF NOT (EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND user_id = NEW.raised_by_user_id)
     OR person_holds_post(NEW.raised_by_user_id, NEW.society_id)) THEN
    PERFORM auth_refuse('raiser_is_member_or_officer', 'a withdrawal request is raised by the member it pays, or by an officer of the society for them');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_withdrawal_r2_raiser BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION withdrawal_request_raiser_admissible();
CREATE FUNCTION withdrawal_request_window() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.kind <> 'withdrawal_out' THEN
    RETURN NEW;
  END IF;
  -- Server time, and the society's window: a client choosing how long its own
  -- request stays live is the requester choosing the window it is judged in.
  NEW.raised_at := now();
  NEW.expires_at := now() + make_interval(days => (SELECT withdrawal_request_days FROM societies WHERE id = NEW.society_id));
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_withdrawal_r3_window BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION withdrawal_request_window();
CREATE FUNCTION resubmission_same_member() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.resubmits_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM approval_requests o
       WHERE o.id = NEW.resubmits_id AND o.kind = NEW.kind AND o.member_id IS NOT DISTINCT FROM NEW.member_id) THEN
    PERFORM auth_refuse('resubmission_same_member', 'a resubmission replaces a rejected request for the same member and of the same kind');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
-- Named to fire AFTER 0007's approval_requests_resubmission: a resubmission
-- that is not of a rejected request of this society is that rule's to refuse.
CREATE TRIGGER approval_requests_resubmission_same_member BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION resubmission_same_member();
CREATE FUNCTION withdrawal_request_within_withdrawable() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  q        RECORD;
  v_floor  BIGINT;
  v_before BIGINT;
  v_held   BIGINT;
  v_open   BIGINT;
  v_person UUID;
  v_out    BIGINT;
BEGIN
  IF NEW.kind <> 'withdrawal_out' OR NEW.member_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO q FROM charge_quote(NEW.society_id, 'withdrawal_out', NEW.amount_kobo, current_date);
  SELECT account_floor_kobo INTO v_floor FROM societies WHERE id = NEW.society_id;
  IF v_floor IS NULL THEN
    PERFORM auth_refuse('account_floor_unset', 'this society has not set its account floor');
  END IF;
  SELECT coalesce(-sum(l.amount_kobo), 0) INTO v_before FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
   WHERE ac.role = 'member_savings' AND l.member_id = NEW.member_id;
  SELECT user_id INTO v_person FROM members WHERE id = NEW.member_id;
  SELECT coalesce(sum(greatest(loan_owed(g.loan_id), 0)), 0) INTO v_held FROM loan_guarantees g
   WHERE (g.guarantor_member_id = NEW.member_id OR g.guarantor_user_id = v_person)
     AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals gw WHERE gw.guarantee_id = g.id);
  SELECT coalesce(sum(h.amount_kobo + h.partner_fee_kobo + h.vat_kobo + h.statutory_levy_kobo + h.argon_margin_kobo), 0) INTO v_open
    FROM withdrawal_holds h
   WHERE h.member_id = NEW.member_id AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
     AND NOT EXISTS (SELECT 1 FROM ledger_entries le WHERE le.hold_id = h.id);
  v_out := NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo;
  IF v_out > v_before - v_held - v_floor - v_open THEN
    PERFORM auth_refuse('withdrawal_request_within_withdrawable', format('%s kobo out (the amount and its charges) against %s withdrawable: savings %s, held for guarantees %s, floor %s, already authorised %s', v_out, v_before - v_held - v_floor - v_open, v_before, v_held, v_floor, v_open));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_withdrawal_r5_withdrawable BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION withdrawal_request_within_withdrawable();
CREATE FUNCTION signature_not_raiser() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN members sm ON sm.id = NEW.member_id
              WHERE r.id = NEW.request_id AND r.raised_by_user_id IS NOT NULL AND r.raised_by_user_id = sm.user_id) THEN
    PERFORM auth_refuse('raiser_never_signs', 'nobody signs a request they raised (LEDGER_CONTRACT §10): one person both asking and approving means nobody checked the other. Another signatory signs it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER signatory_approvals_raiser_never_signs BEFORE INSERT ON signatory_approvals
  FOR EACH ROW EXECUTE FUNCTION signature_not_raiser();
CREATE OR REPLACE FUNCTION operator_set_withdrawal_request_days(p_society UUID, p_days INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old SMALLINT;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  SELECT withdrawal_request_days INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET withdrawal_request_days = p_days WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'withdrawal_request_days', v_old::text, p_days::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;
COMMIT;
