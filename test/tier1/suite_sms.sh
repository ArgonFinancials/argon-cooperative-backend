# Suite sms — the Termii sender (reviewed in four pieces, 2026-10-28), tested
# against a fake Termii server inside the test: no network, no real key.
# Sourced by mutate.sh. The breaks are in the reference sender
# (smsfake_termii_test.go), selected by ARGON_TEST_SMS_FAKE. Run with --svc.
SUITE_INCLUDE='^TestSmsTermii'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestSmsTermii'
SVC_RUN='^TestSmsTermii'
SVC_ENV='ARGON_TEST_SMS_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql'
SUITE_MUTDIR='mutants_sms_none'
SUITE_GREEN_NEEDS='internal/auth/termii.go'      # the real sender, written after the tests
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [dead_as_parse]='TestSmsTermii04_NoConnectionIsNotReachedAndSaysSo'
  [key_optional]='TestSmsTermii09_NoKeyNoSender'
  [log_request_body]='TestSmsTermii08_TheCodeTheKeyTheNumberAndTheTextAreNeverLogged'
  [log_request_error]='TestSmsTermii08_TheCodeTheKeyTheNumberAndTheTextAreNeverLogged'
  [number_guessed]='TestSmsTermii07_ANumberNotInE164IsRefusedBeforeSending'
  [retry_once]='TestSmsTermii02_ARefusalInTheBodyIsARefusal'
  [status_200_success]='TestSmsTermii02_ARefusalInTheBodyIsARefusal'
  [timeout_as_not_reached]='TestSmsTermii05_NoAnswerInTimeIsUnknown'
  [sender_hardcoded]='TestSmsTermii01_AnAcceptedSendCarriesTermiisMessageId'
  [route_generic]='TestSmsTermii01_AnAcceptedSendCarriesTermiisMessageId'
  [status_4xx_unknown]='TestSmsTermii03_ARefusalByStatusIsARefusal'
  [unreadable_as_refused]='TestSmsTermii06_AnUnreadableAnswerIsUnknown'
)
