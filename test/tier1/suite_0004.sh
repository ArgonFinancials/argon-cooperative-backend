# Suite 0004 — account roles. Sourced by mutate.sh.
SUITE_INCLUDE='^TestRole'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestRole'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,test/tier1/0004_interface.sql'
SUITE_MUTDIR='mutants_0004'
SUITE_GREEN_NEEDS='migrations/0004_roles.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestRoleR'
  [r_role_known_but_optional]='TestRoleR1_AccountNeedsARole'
  [r_role_any_text]='TestRoleR1_RoleMustBeKnown'
  [r_only_savings_named]='TestRoleR2_LoanLineNamesAMember'
  [r_member_line_anywhere]='TestRoleR3_MemberLineOnlyOnMemberAccounts'
  [r_charge_on_any_charges_account]='TestRoleR4_PartnerChargeOnlyOnPartnerAccount'
  [r_charge_one_way]='TestRoleR4_ChargeAccountLineIsACharge'
  [r_role_final_always]='TestRole_Control_UnusedAccountMayBeRelabelled'
  [r_charge_lines_refused]='TestRole_Control_ContributionWithEveryRoleRight'
)
