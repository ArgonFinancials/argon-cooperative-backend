-- DELIBERATELY WRONG. Only the guarantor may withdraw, but they may do so after the money has gone out.
--
-- Must turn red: TestLoan19_NoWithdrawalAfterDisbursement.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT actor_user_id FROM credential_acts WHERE id = NEW.act_id) <> (SELECT guarantor_user_id FROM loan_guarantees WHERE id = NEW.guarantee_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_withdrawal_is_own_act'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantee_withdrawals FOR EACH ROW EXECUTE FUNCTION m_f();
