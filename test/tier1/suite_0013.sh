# Suite 0013 — VAT is charged on Argon's own fee, and the names say so
# (decided 2026-10-13). Sourced by mutate.sh.
SUITE_INCLUDE='^TestVAT'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestVAT'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,test/tier1/0013_interface.sql'
SUITE_MUTDIR='mutants_0013'
SUITE_GREEN_NEEDS='migrations/0013_vat_on_argon_fee.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
# M0 is the rename alone: VAT still on the partner's fee, the vat line unpaired.
# Tests 03 and 04 (the old names refused) hold on M0, because refusing them IS
# the rename; each is proved by its own mutant.
declare -gA TARGET_OF=(
  [M0]='TestVAT0[1256]_'
  [m_vat_on_partner_fee]='TestVAT01_VATIsChargedOnArgonsFeeNotThePartners|TestVAT02_VATFollowsArgonsFee|TestVAT06_AHoldsVATIsOnArgonsFee'
  [m_vat_on_amount]='TestVAT02_VATFollowsArgonsFee'
  [m_old_component_allowed]='TestVAT03_TheOldVATComponentIsRefused'
  [m_old_role_allowed]='TestVAT04_AVATLineIsNamedVAT'
  [m_vat_unpaired]='TestVAT05_AVATLineBelongsOnTheVATAccount'
  [c_vat_line_refused]='TestVAT_Control_AVATLineOnTheVATAccount'
)
