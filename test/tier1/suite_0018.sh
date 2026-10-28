# Suite 0018 — one arithmetic for what a member may withdraw: member_position
# and member_guarantees_held, called by the raise check, the hold check and the
# API (decided 2026-10-28, balance handlers piece 1). Sourced by mutate.sh.
SUITE_INCLUDE='^TestPosition'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestPosition'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,test/tier1/0018_interface.sql'
SUITE_MUTDIR='mutants_0018'
SUITE_GREEN_NEEDS='migrations/0018_member_position.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestPosition0'
  [m_pos_guarantee_missing]='TestPosition01_ThePartsAndTheFigure'
  [m_pos_floor_dropped]='TestPosition01_ThePartsAndTheFigure'
  [m_pos_holds_twice]='TestPosition01_ThePartsAndTheFigure'
  [m_pos_repaid_listed]='TestPosition03_ARepaidLoanIsNotHeld'
  [m_pos_negative]='TestPosition05_NeverBelowZero'
)
