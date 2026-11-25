# Suite 0041 — the cooperative's own transfer reference (COOP-), and no payout
# into an account at VFD (2026-11-25, the reviewer, on the wallet platform's
# review). M0: test/tier1/0041_interface.sql (gen0041.py).
SUITE_INCLUDE='^TestCoopRef'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestCoopRef'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,migrations/0036_founding_officers_active.up.sql,migrations/0037_defaults_said.up.sql,migrations/0038_member_status.up.sql,migrations/0039_vfd_as_proven.up.sql,migrations/0040_member_bank_accounts.up.sql,test/tier1/0041_interface.sql'
SUITE_MUTDIR='mutants_0041'
SUITE_GREEN_NEEDS='migrations/0041_coop_reference.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestCoopRef0[12]_'
  [m_argon_prefix]='TestCoopRef01_ATransferReferenceIsTheCooperativesOwn'
  [m_vfd_bank_ok]='TestCoopRef02_NoBankDetailsAtVFD'
  [c_no_bank_taken]='TestCoopRef_Control_AnotherBanksDetailsAreTaken'
)
