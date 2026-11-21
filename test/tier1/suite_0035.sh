# Suite 0035 — demonstration societies (2026-11-21). M0 is the mark and the
# operator's PIN with every rule off (0035_interface.sql, generated with the
# mutants from the migration: test/tier1/gen0035.py).
SUITE_INCLUDE='^TestDemo[0-9_]'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestDemo[0-9_]'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,test/tier1/0035_interface.sql'
SUITE_MUTDIR='mutants_0035'
SUITE_GREEN_NEEDS='migrations/0035_demo_societies.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestDemo0[1-6]_'
  [m_demo_insertable]='TestDemo01_ADemonstrationIsMadeByItsFunctionAndSaysTestData'
  [m_demo_unmarked]='TestDemo01_ADemonstrationIsMadeByItsFunctionAndSaysTestData'
  [m_demo_reversible]='TestDemo02_ADemonstrationStaysOneAndKeepsSayingSo'
  [m_demo_any_society]='TestDemo03_ASocietyPeopleBelongToIsNeverMadeADemonstration'
  [m_demo_any_name]='TestDemo04_ADemonstrationIsNamedTestData'
  [m_pin_for_anyone]='TestDemo05_TheOperatorSetsAPinOnlyForADemonstrationsPeople'
  [m_demo_only_any_membership]='TestDemo05_TheOperatorSetsAPinOnlyForADemonstrationsPeople'
  [m_demo_only_nobody]='TestDemo05_TheOperatorSetsAPinOnlyForADemonstrationsPeople'
  [m_pin_unattributed]='TestDemo06_TheOperatorSaysWhoAndWhy'
  [c_nobody_demo]='TestDemo_Control_ADemonstrationsPersonGetsTheOperatorsPin'
)
