# Suite enrol — redeeming an enrolment code in the auth SERVICE
# (auth.Service.Enrol, approved 2026-11-07). Sourced by mutate.sh. No SQL
# baseline: the schema is the real 0001–0025. The mutants are the named
# breaks of the reference in authfake_enrol_test.go. Run with --svc.
SUITE_INCLUDE='^TestRedeem'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestRedeem'
SVC_RUN='^TestRedeem'
SVC_ENV='ARGON_TEST_AUTH_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql'
SUITE_MUTDIR='mutants_enrol_none'
SUITE_GREEN_NEEDS='internal/auth/enrol.go'      # the real Enrol, written after the tests are shown red
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [enrol_sets_pin_for_holder]='TestRedeem06_ACodeNeverResetsAPin'
  [enrol_skips_pin_rule]='TestRedeem02_ARefusedFirstPinLeavesTheCodeLive'
  [enrol_burns_code]='TestRedeem02_ARefusedFirstPinLeavesTheCodeLive|TestRedeem03_AMissingFirstPinLeavesTheCodeLive|TestRedeem06_ACodeNeverResetsAPin'
  [enrol_code_before_pin]='TestRedeem05_AWrongPinNeverTouchesTheCode'
  [enrol_pin_tries_free]='TestRedeem05_AWrongPinNeverTouchesTheCode'
  [enrol_code_tries_as_pin]='TestRedeem07_AWrongCodeCostsACodeTryAndLooksLikeAnUnknownNumber|TestRedeem08_ARightPinWithAWrongCodeEnrolsNothing'
  [enrol_ignores_lock]='TestRedeem09_ALockedAccountDoesNotEnrol'
  [enrol_wrong_code_told]='TestRedeem07_AWrongCodeCostsACodeTryAndLooksLikeAnUnknownNumber'
  [enrol_used_silent]='TestRedeem10_TheUsedSlipTellsItsHolderWhatToDo'
  [enrol_device_unbound]='TestRedeem01_AFirstEnrolmentSetsThePinAndTrustsTheDevice|TestRedeem11_TheEnrolledDeviceSignsInWithThePinAlone'
  [enrol_no_notice]='TestRedeem04_AHolderEnrolsWithTheirPinAndIsTold'
)
