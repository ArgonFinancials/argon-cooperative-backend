# Suite 0007 — approvals are a control, not a record (PRD 8.4; findings 14
# and 15). Sourced by mutate.sh. The status is derived from signatures,
# rejection rows and expiry; nothing can write it.
SUITE_INCLUDE='^TestApproval'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApproval'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,test/tier1/0007_interface.sql'
SUITE_MUTDIR='mutants_0007'
SUITE_GREEN_NEEDS='migrations/0007_approvals.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestApproval[0-9]'
  [m_column_still_writable]='TestApproval1_StatusCannotBeWritten'
  [m_column_trusted_when_approved]='TestApproval2_SettingTheColumnApprovesNothing'
  [m_quorum_one_short]='TestApproval3_ApprovedAtQuorumNotBefore'
  [m_signing_checks_rejection_only]='TestApproval4_SigningAnExpiredRequestIsRefused'
  [m_never_expires]='TestApproval5_UnapprovedPastExpiryReadsExpired'
  [m_expiry_beats_approval]='TestApproval6_ApprovedStaysApprovedAfterExpiry'
  [m_rejections_ignored]='TestApproval7_RejectionClosesTheRequest'
  [m_reason_any_text]='TestApproval8_RejectionGivesOneOfTheFourReasons'
  [m_rejected_at_unchecked]='TestApproval9_RejectionTimeIsTheServers'
  [m_rejecter_any_current_post]='TestApproval10_RejecterHoldsACurrentSigningPost'
  [m_rejecter_user_unchecked]='TestApproval11_RejecterIsThePersonOfTheMemberRecord'
  [m_signing_checks_expiry_only]='TestApproval12_SigningARejectedRequestIsRefused'
  [m_reject_unless_expired]='TestApproval13_RejectingAnApprovedRequestIsRefused'
  [m_reject_unless_approved]='TestApproval14_RejectingAnExpiredRequestIsRefused'
  [m_rejection_update_only]='TestApproval15_RejectionsAreImmutable'
  [c_quorum_needs_one_more]='TestApproval_Control_TwoOfThreeApproves'
  [c_fraud_reason_refused]='TestApproval_Control_EachOfTheFourReasons'
  [m_signer_may_reject]='TestApproval17_ASignerCannotThenReject'
  [m_note_not_null_only]='TestApproval18_LooksWrongRequiresANote'
  [m_resubmits_same_society_only]='TestApproval19_ResubmissionPointsAtARejectedRequest'
  [c_note_for_every_reason]='TestApproval_Control_NoNoteNeededExceptForLooksWrong'
  [c_resubmission_refused]='TestApproval_Control_ResubmittingARejectedRequest'
)
