-- DELIBERATELY WRONG. A guarantor is barred from signing only until the money has gone out: the write-off, which comes after, is theirs to vote on.
--
-- Must turn red: TestWriteOff7_AGuarantorNeverSignsTheWriteOff.
CREATE OR REPLACE FUNCTION signature_not_guarantor() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN loan_guarantees g ON g.loan_id = r.loan_id
              WHERE r.id = NEW.request_id AND NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = r.loan_id AND kind = 'loan_disbursement')
                AND g.guarantor_user_id = (SELECT user_id FROM members WHERE id = NEW.member_id)) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_guarantor';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
