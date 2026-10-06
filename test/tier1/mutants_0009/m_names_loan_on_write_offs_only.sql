-- DELIBERATELY WRONG. A write-off must name its loan; a recovery need not.
--
-- Must turn red: TestWriteOff16_WriteOffsAndRecoveriesNameTheirLoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND NEW.loan_id IS NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_movement_names_loan'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
