-- DELIBERATELY WRONG. OVER-STRICT: a rejection must rest on a SIGNATURE act, so no one can reject without first taking the step-up to sign.
--
-- Must turn red: TestSignStep_Control_RejectingWithTheirOwnStepUp.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE a credential_acts%ROWTYPE; BEGIN
  IF NEW.act_id IS NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_needs_step_up'; END IF;
  IF NEW.act_id IS NOT NULL THEN
    SELECT * INTO a FROM credential_acts WHERE id = NEW.act_id;
    IF a.actor_user_id IS DISTINCT FROM NEW.user_id THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_act_is_rejecters'; END IF;
    IF a.kind <> 'approval_signature' THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_act_kind'; END IF;
    IF a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_act_same_transaction'; END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
CREATE UNIQUE INDEX rejection_act_once ON approval_rejections (act_id);
