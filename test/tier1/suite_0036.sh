# Suite 0036 — a founding officer is an active member (2026-11-22). M0 is the
# founding function leaving the default and a backfill that does nothing
# (0036_interface.sql, generated with the mutants: test/tier1/gen0036.py).
SUITE_INCLUDE='^TestOfficerActive'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestOfficerActive'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,test/tier1/0036_interface.sql'
SUITE_MUTDIR='mutants_0036'
SUITE_GREEN_NEEDS='migrations/0036_founding_officers_active.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestOfficerActive0[1-3]_'
  [m_founding_pending]='TestOfficerActive01_AFoundingOfficerIsAnActiveMember'
  [m_backfill_nothing]='TestOfficerActive02_TheFoundingOfficersAlreadyAppointedAreMadeActive'
  [m_backfill_any_status]='TestOfficerActive03_AStatusSomeoneChoseIsNotOverwritten'
  [c_backfill_everyone]='TestOfficerActive_Control_OnlyFoundingOfficersAreTouched'
)
