# Suite stepup — the PIN step-up in the auth SERVICE (auth.Service.ProvePIN).
# Sourced by mutate.sh. No SQL baseline: the schema is the real 0001–0004 plus
# the 0005 interface (proofs carry an act kind). The mutants are the named
# breaks of the reference in authfake_stepup_test.go. Run with --svc.
SUITE_INCLUDE='^TestStepUpS'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestStepUpS'
SVC_RUN='^TestStepUpS'
SVC_ENV='ARGON_TEST_AUTH_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,test/tier1/0005_interface.sql'
SUITE_MUTDIR='mutants_stepup_none'
SUITE_GREEN_NEEDS='internal/auth/stepup.go'      # the real ProvePIN, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [stepup_not_counted]='TestStepUpS2_WrongPinMintsNothingAndCountsAsAFailure|TestStepUpS3_StepUpFailuresLockSignIn'
  [stepup_own_counter]='TestStepUpS3_StepUpFailuresLockSignIn|TestStepUpS4_SignInFailuresLockStepUp|TestStepUpS5_MixedFailuresShareOneCount'
  [stepup_ignores_lock]='TestStepUpS4_SignInFailuresLockStepUp'
  [stepup_mints_anyway]='TestStepUpS2_WrongPinMintsNothingAndCountsAsAFailure'
  [stepup_unscoped]='TestStepUpS1_RightPinMintsOneProofForTheHolderAndTheAct'
  [stepup_any_act]='TestStepUpS7_UnknownActNoProof'
  [stepup_ended_session]='TestStepUpS6_NoLiveSessionNoProof'
  [stepup_expiry_wrong]='TestStepUpS1_RightPinMintsOneProofForTheHolderAndTheAct'
  [stepup_success_silent]='TestStepUpS8_RightPinAtStepUpEndsTheRun'
  [stepup_ignores_success]='TestStepUpS9_RightPinAtSignInEndsAStepUpRun'
)
