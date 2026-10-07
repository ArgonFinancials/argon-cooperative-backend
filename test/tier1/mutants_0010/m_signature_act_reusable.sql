-- DELIBERATELY WRONG. Every check holds, but one act may stand behind any number of signatures.
--
-- Must turn red: TestSignStep5_OneStepUpSignsOnce.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE a credential_acts%ROWTYPE; BEGIN
  IF NEW.act_id IS NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_needs_step_up'; END IF;
  IF NEW.act_id IS NOT NULL THEN
    SELECT * INTO a FROM credential_acts WHERE id = NEW.act_id;
    IF a.actor_user_id IS DISTINCT FROM (SELECT user_id FROM members WHERE id = NEW.member_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_act_is_signers'; END IF;
    IF a.kind <> 'approval_signature' THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_act_kind'; END IF;
    IF a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_act_same_transaction'; END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION m_f();
