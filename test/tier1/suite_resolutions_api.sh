# Suite resolutions_api — the officers' payment endpoints over HTTP (stage 4,
# 2026-11-11): who reads and who acts, the unmatched payments with their
# claims, two officers' resolution, the key, the open proposal named, every
# refusal, the state filter. The breaks are in the reference handlers
# (apifake_resolutions_test.go), selected by ARGON_TEST_API_FAKE. Run with --svc.
SUITE_INCLUDE='^TestApiResolve'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiResolve'
SVC_RUN='^TestApiResolve'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/resolutions.go'
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [resolve_any_reader]='TestApiResolve01_OfficersReadTheAuditorReadsOnlyOfficersAct'
  [resolve_auditor_blind]='TestApiResolve01_OfficersReadTheAuditorReadsOnlyOfficersAct'
  [unmatched_claims_hidden]='TestApiResolve02_AnUnmatchedPaymentComesWithEveryClaimOnIt'
  [unmatched_credited_listed]='TestApiResolve02_AnUnmatchedPaymentComesWithEveryClaimOnIt'
  [entry_hidden]='TestApiResolve03_ASecondOfficerConfirmsAndTheContributionIsAnswered'
  [propose_key_ignored]='TestApiResolve04_ARepeatedKeyAnswersTheFirstProposal'
  [open_unnamed]='TestApiResolve05_AnOpenProposalIsNamed'
  [refusal_as_500]='TestApiResolve06_EveryRefusalSaysWhich'
  [decided_as_500]='TestApiResolve06_EveryRefusalSaysWhich'
  [state_filter_ignored]='TestApiResolve07_TheListFiltersByState'
)
