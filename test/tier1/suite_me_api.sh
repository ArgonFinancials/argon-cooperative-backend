# Suite me_api — GET /me: who the caller is, the join between sign-in and
# every other endpoint (finding 40). Sourced by mutate.sh. Run with --svc.
SUITE_INCLUDE='^TestApiMe[0-9]'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiMe[0-9]'
SVC_RUN='^TestApiMe[0-9]'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/me.go'          # the real handler, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [me_status_fixed]='TestApiMe01_AMemberSeesTheirOwnRecord'
  [me_null_lists]='TestApiMe01_AMemberSeesTheirOwnRecord|TestApiMe07_NoMemberRecordIsAnEmptyList'
  [me_signatory_dropped]='TestApiMe02_AnOfficerSeesTheirCurrentPost'
  [me_posts_ever]='TestApiMe03_AnEndedPostIsNotListed'
  [me_future_posts]='TestApiMe04_APostNotYetBegunIsNotListed'
  [me_first_society_only]='TestApiMe05_APersonInTwoSocietiesSeesBoth'
  [me_unordered]='TestApiMe05_APersonInTwoSocietiesSeesBoth'
  [me_any_member]='TestApiMe06_OnlyTheCallersRecords'
  [me_public]='TestApiMe08_NoSessionIsRefused'
  [ended_session_ok]='TestApiMe09_AnEndedSessionIsRefused'
)
