# Suite sms_api — a new device gets its code through Termii: what the member
# is told in each outcome, what is recorded, and that our failure counts
# nothing against them (the Termii sender, piece 2, 2026-10-28). Sourced by
# mutate.sh. The breaks are in the reference sign-in service and API
# (authfake_test.go, apifake_signin_test.go). Run with --svc.
SUITE_INCLUDE='^TestApiSms'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiSms'
SVC_RUN='^TestApiSms'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/signin_resend.go'      # the resend minute's answer (2026-10-30), written after the tests
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [refusal_to_officer]='TestApiSms05_ARefusalIsNeverRoutedToAnOfficer'
  [refused_as_sent]='TestApiSms02_ARefusedCodeIsNotSentAndCountsNothing'
  [send_failure_counted]='TestApiSms02_ARefusedCodeIsNotSentAndCountsNothing|TestApiSms03_AnUnreachedProviderIsNotSentAndCountsNothing'
  [send_unrecorded]='TestApiSms01_AnAcceptedCodeIsSentRecordedAndWorks'
  [unknown_as_not_sent]='TestApiSms04_AnUnknownSendKeepsTheChallengeAndSaysItMayBeLate'
  [unknown_unsaid]='TestApiSms04_AnUnknownSendKeepsTheChallengeAndSaysItMayBeLate'
  [sms_unset_refusing]='TestApiSms06_TheConfigurationNamesTheSender'
  [termii_key_optional]='TestApiSms06_TheConfigurationNamesTheSender'
  [termii_plain_http]='TestApiSms06_TheConfigurationNamesTheSender'
  [resend_as_lockout]='TestApiSms07_TooSoonForAnotherCodeIsNotALockout'
  [resend_counted]='TestApiSms07_TooSoonForAnotherCodeIsNotALockout'
  [resend_says_attempts]='TestApiSms07_TooSoonForAnotherCodeIsNotALockout'
  [lockout_as_resend]='TestApiSms07_TooSoonForAnotherCodeIsNotALockout'
)
