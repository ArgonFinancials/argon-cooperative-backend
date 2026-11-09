# Suite 0026 — a signatory's code comes in two halves, from two other current
# signatories (decided 2026-11-06, confirmed 2026-11-09). M0 is the halves'
# columns and the server's assembly with no rules.
SUITE_INCLUDE='^TestHalves'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestHalves'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,test/tier1/0026_interface.sql'
SUITE_MUTDIR='mutants_0026'
SUITE_GREEN_NEEDS='migrations/0026_enrolment_halves.up.sql'   # written after the red half
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  # 06 holds on M0, which assembles so the controls can redeem; its proof is
  # a mutant that assembles in one order only. 09 is the base's own counting;
  # its proof is a mutant that does not count tries on a halved code.
  [M0]='TestHalves0[1-578]'
  [m_signatory_whole_allowed]='TestHalves01_ASignatorysCodeFromAnOfficerComesInTwoHalves'
  [m_first_half_by_any_officer]='TestHalves02_EachHalfIsASignatorys'
  [m_second_half_by_any_officer]='TestHalves02_EachHalfIsASignatorys'
  [m_one_signatory_both_halves]='TestHalves03_TheHalvesComeFromTwoSignatories'
  [m_second_half_any_act]='TestHalves04_TheSecondHalfRestsOnItsOwnAct'
  [m_pending_tried]='TestHalves05_OneHalfAloneRedeemsNothingAndCostsNoTry'
  [m_order_matters]='TestHalves06_TheServerAssemblesTheCode'
  [m_second_half_replaceable]='TestHalves07_AHalfIsSetOnceAndNeverChanged'
  [m_first_half_replaceable]='TestHalves07_AHalfIsSetOnceAndNeverChanged'
  [m_two_shapes]='TestHalves08_ACodeIsWholeOrHalvedNeverBoth'
  [m_tries_not_on_halved]='TestHalves09_FiveWrongTriesVoidTheAssembledCode'
  [c_completion_refused]='TestHalves_Control_TwoSignatoriesIssueASignatorysCode'
  [c_operator_halves_signatories]='TestHalves_Control_TheOperatorStillIssuesAFoundingSignatorysCode'
  [c_everyone_halved]='TestHalves_Control_AMemberStillGetsAWholeCode'
)
