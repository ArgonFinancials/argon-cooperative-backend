# Suite 0006 — a line posts to an account of its entry's society (§3: the
# trial balance sums to zero by construction). Sourced by mutate.sh.
# Found specifying the trial balance; see tier1_0006_test.go.
SUITE_INCLUDE='^TestLineSociety'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestLineSociety'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql'
SUITE_MUTDIR='mutants_0006'
SUITE_GREEN_NEEDS='migrations/0006_line_society.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestLineSociety[0-9]'
  [a_principal_lines_only]='TestLineSociety2_ChargeLineCannotPostToAnotherSocietysChargesAccount'
  [a_member_accounts_exempt]='TestLineSociety3_MemberLineCannotPostToAnotherSocietysSavings'
  [a_member_accounts_only]='TestLineSociety1_LineCannotPostToAnotherSocietysAccount|TestLineSociety2_ChargeLineCannotPostToAnotherSocietysChargesAccount'
  [c_member_society_required]='TestLineSociety_Control_OwnAccountsAccepted'
)
