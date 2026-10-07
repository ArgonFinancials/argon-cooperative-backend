# Suite approvals_api — view, sign and reject a request over HTTP (PRD 8.4;
# 0007, 0010): two projections, the signer's own step-up, the fraud path.
# Sourced by mutate.sh. Run with --svc.
#
# Base: the real 0001–0009 and 0010's interface (the act columns, no rules):
# the step-up is proven at the database in suite 0010; here, at the API.
SUITE_INCLUDE='^TestApiApprovals'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiApprovals'
SVC_RUN='^TestApiApprovals'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,test/tier1/0010_interface.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/approvals.go'          # the real handlers, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [appr_officer_unnamed]='TestApiApprovals1_AnOfficerSeesWhoSigned'
  [appr_auditor_refused]='TestApiApprovals2_TheAuditorReadsTheOfficerView'
  [appr_member_sees_names]='TestApiApprovals3_TheMemberSeesProgressNotNames'
  [appr_any_member_reads]='TestApiApprovals4_AnyoneElseGetsNotFound'
  [appr_officer_any_society]='TestApiApprovals4_AnyoneElseGetsNotFound'
  [appr_not_found_says_why]='TestApiApprovals4_AnyoneElseGetsNotFound'
  [appr_sign_without_step_up]='TestApiApprovals5_SigningNeedsAStepUp'
  [appr_status_stale]='TestApiApprovals6_TheAnswerCarriesTheNewStatus'
  [appr_failed_step_up_signs]='TestApiApprovals7_AProofSignsOnce'
  [appr_not_signatory_as_404]='TestApiApprovals8_TheAuditorCannotSign|TestApiApprovals17_ARefusedSignerIsNotToldTheRequestIsMissing'
  [appr_closed_as_403]='TestApiApprovals9_SigningAnExpiredRequestIsRefused'
  [appr_already_signed_as_closed]='TestApiApprovals10_SigningTwiceIsRefused'
  [appr_reject_without_step_up]='TestApiApprovals11_RejectingNeedsAStepUp'
  [appr_rejection_note_dropped]='TestApiApprovals12_ARejectionShowsOfficersItsReasonNoteAndAuthor'
  [appr_note_refusal_as_500]='TestApiApprovals13_LooksWrongNeedsANote'
  [appr_signed_reject_as_closed]='TestApiApprovals14_ASignerCannotThenReject'
  [appr_member_sees_rejecter]='TestApiApprovals15_TheMemberSeesWhyNotWho'
  [appr_fraud_path_shown]='TestApiApprovals16_TheFraudPathIsNotShownToTheMember'
  [money_as_numbers]='TestApiApprovals18_MoneyIsAStringOnTheWire'
)
