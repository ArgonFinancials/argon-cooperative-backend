# Suite 0027 — the founding officers, appointed by the operator on the
# founding resolution (stage 1, 2026-11-10). M0 is the operator's function
# with no rules.
SUITE_INCLUDE='^TestFirstOfficer'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestFirstOfficer'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,test/tier1/0027_interface.sql'
SUITE_MUTDIR='mutants_0027'
SUITE_GREEN_NEEDS='migrations/0027_founding_officers.up.sql'   # written after the red half
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestFirstOfficer0[1-5]'
  [m_no_operator]='TestFirstOfficer01_TheOperatorNamesThemselvesTheLoginAndWhy'
  [m_no_login]='TestFirstOfficer01_TheOperatorNamesThemselvesTheLoginAndWhy'
  [m_no_reason]='TestFirstOfficer01_TheOperatorNamesThemselvesTheLoginAndWhy'
  [m_no_resolution]='TestFirstOfficer02_ItRestsOnTheResolution'
  [m_future_ok]='TestFirstOfficer02_ItRestsOnTheResolution'
  [m_unidentified]='TestFirstOfficer03_TheOfficerIsIdentified'
  [m_any_lifecycle]='TestFirstOfficer04_OnlyWhileTheSocietyIsBeingFounded'
  [m_not_logged]='TestFirstOfficer05_OneActWritesThePersonTheMemberThePostAndTheLog'
  [m_held_from_today]='TestFirstOfficer05_OneActWritesThePersonTheMemberThePostAndTheLog'
  [c_prospect_refused]='TestFirstOfficer_Control_TheOperatorThenIssuesTheirCode'
)
