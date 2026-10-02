# Suite 0005 — the PIN step-up: proofs scoped at mint, never minted where they
# are used, and a ledger batch authorised by its own act. Sourced by mutate.sh.
# (The ROLE half of "not mintable by the path that uses it" is in the probe,
# deploy/70-verify-tier1.sh P11–P14: privileges live in grants.sql.)
SUITE_INCLUDE='^TestProof'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestProof'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,test/tier1/0005_interface.sql'
SUITE_MUTDIR='mutants_0005'
SUITE_GREEN_NEEDS='migrations/0005_step_up.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestProof[0-9]'
  [p_kind_optional]='TestProof1_PinProofNamesItsActAtMint'
  [p_kind_any_text]='TestProof2_ActKindMustBeKnown'
  [p_kind_not_compared]='TestProof3_ProofForAnImportDoesNotAuthoriseAPinChange|TestProof3_ProofForAPinChangeDoesNotAuthoriseAnImport'
  [p_kind_import_only]='TestProof3_ProofForAnImportDoesNotAuthoriseAPinChange'
  [p_same_tx_by_time]='TestProof4_ProofCannotBeMintedAndUsedInOneTransaction'
  [b_act_optional]='TestProof5_BatchNeedsAnAuthorisingAct'
  [b_act_any_kind]='TestProof6_BatchActIsALedgerBatchAct'
  [b_act_any_actor]='TestProof7_BatchActIsTheActorsOwn'
  [b_act_any_transaction]='TestProof8_BatchActIsFromThisTransaction'
  [b_act_reusable]='TestProof9_OneActAuthorisesOneBatch'
  [c_batch_act_elsewhere]='TestProof_Control_ScopedProofAuthorisesItsBatch'
  [c_every_act_needs_import_proof]='TestProof_Control_ExistingActWithItsScopedProof'
)
