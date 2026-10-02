# Suite signin_api — sign-in over HTTP (POST /auth/sign-in, /auth/codes/verify,
# /auth/sign-out) and the refusing SMS adapter. Sourced by mutate.sh. No SQL
# baseline: the schema is the real 0001-0005. The mutants are the named breaks
# in apifake_signin_test.go (and the shared ones in apifake_test.go and the
# statement fake). Run with --svc.
SUITE_INCLUDE='^TestApiSignIn'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiSignIn'
SVC_RUN='^TestApiSignIn'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql'
SUITE_MUTDIR='mutants_signin_api_none'
SUITE_GREEN_NEEDS='internal/api/signin.go'      # the real handlers, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [member_refused]='TestApiSignIn01_KnownDeviceSignsInAndReachesTheMembersOwnStatement'
  [no_token_reaches]='TestApiSignIn02_WithoutASessionNothingIsReachable'
  [signout_noop]='TestApiSignIn03_SignedOutSessionReachesNothing'
  [ended_session_ok]='TestApiSignIn03_SignedOutSessionReachesNothing|TestApiSignIn04_ExpiredSessionReachesNothing'
  [officer_any_society]='TestApiSignIn05_SessionDoesNotReachAnotherSociety'
  [unknown_says_why]='TestApiSignIn06_UnknownCredentialAndWrongPinAnswerIdentically'
  [unknown_skips_hash]='TestApiSignIn07_RefusalTimingDoesNotRevealRegistration'
  [shared_first_match]='TestApiSignIn08_SharedPhoneAcrossSocietiesSignsNobodyIn'
  [expiry_from_surface]='TestApiSignIn09_ApproversSessionIsEightHoursOnAnySurface'
  [sms_failure_500]='TestApiSignIn10_NewDeviceWithoutSMSGoesToAnOfficerAndSaysWhy'
  [sms_failure_silent]='TestApiSignIn10_NewDeviceWithoutSMSGoesToAnOfficerAndSaysWhy'
  [sms_adapter_silent]='TestApiSignIn11_RefusingSMSRefusesLoudlyAndSaysItIsTemporary'
  [sms_adapter_quiet]='TestApiSignIn11_RefusingSMSRefusesLoudlyAndSaysItIsTemporary'
  [sms_adapter_sounds_final]='TestApiSignIn11_RefusingSMSRefusesLoudlyAndSaysItIsTemporary'
  [verify_any_code]='TestApiSignIn12_NewDeviceCodeCompletesSignIn'
  [signin_ignores_lock]='TestApiSignIn13_LockedAccountIsTryLaterEvenWithTheRightPin'
  [router_errors_plain]='TestApiSignIn14_MalformedRequestsAnswerTheOneErrorFormat'
)
