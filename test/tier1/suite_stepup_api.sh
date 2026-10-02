# Suite stepup_api — the PIN step-up over HTTP: POST /auth/pin-proofs, and
# postAll spending its proof. Sourced by mutate.sh. No SQL baseline: the schema
# is the real 0001–0004 plus the 0005 interface. The mutants are the named
# breaks in apifake_stepup_test.go and apifake_batch_test.go. Run with --svc.
SUITE_INCLUDE='^TestApiStepUp'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiStepUp'
SVC_RUN='^TestApiStepUp'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,test/tier1/0005_interface.sql'
SUITE_MUTDIR='mutants_stepup_api_none'
SUITE_GREEN_NEEDS='internal/api/stepup.go'      # the real handler, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [pin_proof_unscoped]='TestApiStepUp01_PinProofIsMintedForTheCallerAndTheAct'
  [pin_proof_any_pin]='TestApiStepUp02_WrongPinMintsNothingAndIsCounted'
  [pin_proof_ignores_lock]='TestApiStepUp03_LockedAccountMintsNothing'
  [ended_session_ok]='TestApiStepUp04_NoValidSessionMintsNothing'
  [pin_proof_any_act]='TestApiStepUp05_UnknownActMintsNothing'
  [proof_optional]='TestApiStepUp06_BatchWithoutAProofWritesNothingAndMintsNothing'
  [batch_mints_own_proof]='TestApiStepUp06_BatchWithoutAProofWritesNothingAndMintsNothing'
  [proof_any_act]='TestApiStepUp07_ProofForAnotherActIsRefused'
  [proof_reusable]='TestApiStepUp08_SpentProofIsRefused|TestApiStepUp14_CommittedBatchSpendsItsProofThroughItsOwnAct'
  [proof_never_stale]='TestApiStepUp09_StaleProofIsRefused'
  [proof_anyones]='TestApiStepUp10_AnotherPersonsProofIsRefused'
  [repeat_needs_proof]='TestApiStepUp11_RepeatNeedsNoProof'
  [refusal_spends_proof]='TestApiStepUp12_RefusedBatchKeepsTheProof'
  [failure_spends_proof]='TestApiStepUp13_FailedBatchKeepsTheProof'
  [batch_unlinked]='TestApiStepUp14_CommittedBatchSpendsItsProofThroughItsOwnAct'
  [unconfigured_500]='TestApiStepUp15_UnconfiguredServerSaysSoAndMintsNothing'
)
