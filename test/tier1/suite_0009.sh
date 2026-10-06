# Suite 0009 — write-offs (LOAN_SHAPE.md §6a, approved 2026-10-06). Sourced by
# mutate.sh. The quorum is the society's, copied at raise time; neither the
# borrower nor a guarantor signs; the entry is exactly what was approved and
# owed; a recovery is its own movement and never reopens the loan.
SUITE_INCLUDE='^TestWriteOff'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestWriteOff'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,test/tier1/0009_interface.sql'
SUITE_MUTDIR='mutants_0009'
SUITE_GREEN_NEEDS='migrations/0009_write_offs.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestWriteOff[0-9]'
  [m_unset_means_two]='TestWriteOff1_AnUnsetQuorumRefusesAndSaysSo'
  [m_unset_checked_any_required]='TestWriteOff2_TheRequestCarriesTheSocietysQuorum'
  [m_owed_means_disbursed]='TestWriteOff3_OnlyADisbursedLoanWithMoneyOwed'
  [m_request_amount_unchecked]='TestWriteOff4_TheRequestIsForWhatIsOwed'
  [m_reason_null_only]='TestWriteOff5_AWriteOffGivesItsReason'
  [m_borrower_barred_on_write_offs_only]='TestWriteOff6_TheBorrowerNeverSignsARequestForTheirOwnLoan'
  [m_guarantor_barred_while_undisbursed]='TestWriteOff7_AGuarantorNeverSignsTheWriteOff'
  [m_write_off_on_any_signature]='TestWriteOff8_TheEntryNeedsAnApprovedWriteOff'
  [m_write_off_at_most_owed]='TestWriteOff9_TheEntryIsExactlyWhatIsOwed'
  [m_write_off_owed_not_approved]='TestWriteOff10_TheEntryIsWhatWasApproved'
  [m_write_off_approved_but_not_once]='TestWriteOff11_WrittenOffOnce'
  [m_loss_any_non_member_account]='TestWriteOff12_TheLossGoesToALossAccount'
  [m_recovery_checked_repayment_not]='TestWriteOff13_NoRepaymentAfterAWriteOff'
  [m_recovery_on_any_disbursed_loan]='TestWriteOff14_ARecoveryNeedsAWrittenOffLoan'
  [m_recovery_may_reopen]='TestWriteOff15_ARecoveryNeverReopensTheLoan'
  [m_names_loan_on_write_offs_only]='TestWriteOff16_WriteOffsAndRecoveriesNameTheirLoan'
  [c_write_off_after_schedule_only]='TestWriteOff_Control_AnInArrearsLoanWrittenOffThenRecovered'
  [c_status_rereads_quorum]='TestWriteOff_Control_ALaterQuorumDoesNotRejudge'
)
