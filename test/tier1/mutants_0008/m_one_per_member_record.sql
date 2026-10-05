-- DELIBERATELY WRONG. One guarantee per member RECORD: one person with two records counts twice.
--
-- Must turn red: TestLoan16_OneGuaranteePerPerson.
CREATE UNIQUE INDEX loan_guarantees_one_per_person ON loan_guarantees (loan_id, guarantor_member_id);
