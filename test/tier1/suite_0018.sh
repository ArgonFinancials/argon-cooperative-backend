# Suite 0018 — every session carries its own CSRF token (decided 2026-10-23,
# LEDGER_CONTRACT.md §12: a synchronizer token bound to the session, for the
# session cookie). Sourced by mutate.sh.
SUITE_INCLUDE='^TestCsrf'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestCsrf'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,test/tier1/0018_interface.sql'
SUITE_MUTDIR='mutants_0018'
SUITE_GREEN_NEEDS='migrations/0018_session_csrf.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestCsrf0'
  [m_csrf_optional]='TestCsrf01_ASessionWithoutACsrfDigestIsRefused'
  [m_csrf_mutable]='TestCsrf02_ASessionsCsrfDigestNeverChanges'
  [m_csrf_any_text]='TestCsrf03_ACsrfTokenIsStoredAsADigest'
  [m_csrf_same_as_session]='TestCsrf04_TheCsrfTokenIsNotTheSessionToken'
  [c_csrf_no_session]='TestCsrf_Control_ASessionWithItsDigestStartsAndEnds'
)
