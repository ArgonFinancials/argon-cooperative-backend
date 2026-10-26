# Suite spec_api — the spec's lists are the schema's: every enumeration the
# spec writes beside a database type or constraint, compared with what the
# schema allows (2026-10-22, the sixth hand-written list to drift). Sourced by
# mutate.sh. Run with --svc.
SUITE_INCLUDE='^TestApiSpec'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiSpec'
SVC_RUN='^TestApiSpec'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='api/openapi.yaml'          # the spec itself: GREEN reads the spec the server serves
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  # The comparison made to fail: the spec read as if it had lost a value.
  [spec_list_drift]='TestApiSpec01_TheSpecsListsAreTheSchemas'
  # The built marker read wrong (2026-10-26). Step 1: a flag missing. Step 2:
  # a flag flipped with its 501 left (the spec contradicts itself). Step 3, the
  # real server, where flag and 501 agree with each other but not the code: an
  # operation written up as unbuilt while its handler exists (the quiet
  # direction), and one written up as built whose handler was never written.
  [built_flag_missing]='TestApiSpec02_EveryOperationSaysWhetherItIsBuiltAndIsRight'
  [built_flag_flipped]='TestApiSpec02_EveryOperationSaysWhetherItIsBuiltAndIsRight'
  [not_built_answer_kept]='TestApiSpec02_EveryOperationSaysWhetherItIsBuiltAndIsRight'
  [built_without_handler]='TestApiSpec02_EveryOperationSaysWhetherItIsBuiltAndIsRight'
)
