# Suite status_api — a member's status over HTTP (0038, 2026-11-24): the
# secretary suspends, reinstates or records a departure, on their own PIN,
# with a reason; refusals in words before anything is written; the history.
# The breaks are in the reference (apifake_status_test.go). Run with --svc.
SUITE_INCLUDE='^TestApiStatus'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiStatus'
SVC_RUN='^TestApiStatus'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,migrations/0036_founding_officers_active.up.sql,migrations/0037_defaults_said.up.sql,migrations/0038_member_status.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/status.go'
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [status_record_unnamed]='TestApiStatus01_TheSecretarySuspendsAndReinstatesWithAReason'
  [status_any_officer]='TestApiStatus02_OnlyTheSecretary'
  [status_own_db]='TestApiStatus03_TheSecretaryNeverChangesTheirOwn'
  [status_reason_db]='TestApiStatus04_AReasonIsRequiredAndTheRefusalSpendsNothing'
  [status_move_db]='TestApiStatus05_LeavingIsFinal'
  [status_post_db]='TestApiStatus06_AnOfficerInPostIsNotSuspended'
  [status_history_oldest]='TestApiStatus07_TheHistoryIsNewestFirstAndSaysWho'
)
