# Suite pin — what a new PIN may be (ruled 2026-10-30): six digits, numeric,
# and the closed list of trivial PINs. Sourced by mutate.sh. The breaks are in
# the reference rule (pinfake_test.go). Run with --svc.
SUITE_INCLUDE='^TestPin'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestPin'
SVC_RUN='^TestPin'
SVC_ENV='ARGON_TEST_PIN_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/auth/pin.go'      # the rule, written after the tests
CONTROL_RE='Control'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [four_digits_ok]='TestPin01_SixDigitsNumeric'
  [letters_ok]='TestPin01_SixDigitsNumeric'
  [unicode_digits]='TestPin01_SixDigitsNumeric'
  [same_digit_ok]='TestPin02_AllOneDigitIsRefused'
  [run_up_only]='TestPin03_AStraightRunIsRefused'
  [doubled_ok]='TestPin04_DoubledDigitsOfARunAreRefused'
  [phone_first_only]='TestPin05_TheLastSixOfTheMembersOwnPhoneAreRefused'
  [phone_ignored]='TestPin05_TheLastSixOfTheMembersOwnPhoneAreRefused'
  [repeated_pair]='TestPin06_Control_OrdinaryPinsAreAccepted'
  [wraps]='TestPin06_Control_OrdinaryPinsAreAccepted'
)
