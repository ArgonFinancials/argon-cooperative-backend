# Suite 0025 — a redeemed code names the device it trusted; a used slip says
# so to its holder (accepted residual, 2026-11-06). M0 is the redemption with
# no rules, the old one still in place.
SUITE_INCLUDE='^TestSlip'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestSlip'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,test/tier1/0025_interface.sql'
SUITE_MUTDIR='mutants_0025'
SUITE_GREEN_NEEDS='migrations/0025_slip_device.up.sql'   # written after the red half
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  # 02 (a refused redemption trusts no device) is an absence: it holds on M0 by
  # doing nothing; its proof is a mutant that trusts the device first.
  [M0]='TestSlip01|TestSlip0[3-6]'
  [c_first_device_refused]='TestSlip_Control_TheRightSlipEnrols'
  [m_device_before_check]='TestSlip02_ARefusedRedemptionTrustsNoDevice'
  [m_device_unrecorded]='TestSlip01_ARedemptionTrustsOneDeviceAndTheCodeNamesIt'
  [m_old_redemption_kept]='TestSlip06_NoRedemptionWithoutADevice'
  [m_used_code_editable]='TestSlip03_TheBoundDeviceNeverChanges'
  [m_used_said_to_anyone]='TestSlip05_AWrongCodeNeverSaysAlreadyUsed'
  [m_used_slip_counts]='TestSlip05_AWrongCodeNeverSaysAlreadyUsed'
  [m_used_slip_silent]='TestSlip04_AUsedSlipSaysSoToItsHolder'
)
