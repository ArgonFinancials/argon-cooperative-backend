# Suite 0031 — the register: a society's members, registered by its secretary
# (2026-11-10). M0 is register_member with no rules (0031_interface.sql).
SUITE_INCLUDE='^TestRegister'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestRegister'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,test/tier1/0031_interface.sql'
SUITE_MUTDIR='mutants_0031'
SUITE_GREEN_NEEDS='migrations/0031_member_register.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestRegister0[1-9]'
  [m_any_registrar]='TestRegister01_OnlyTheSocietysCurrentSecretaryRegisters'
  [m_any_act_kind]='TestRegister02_ItRestsOnTheSecretarysOwnActFromThisTransaction'
  [m_any_transaction]='TestRegister02_ItRestsOnTheSecretarysOwnActFromThisTransaction'
  [m_any_phone]='TestRegister03_APhoneIsNigerianE164'
  [m_branch_guessed]='TestRegister04_ABranchIsTheSocietysOwn'
  [m_future_ok]='TestRegister05_JoinedIsNotInTheFuture'
  [m_nok_partial]='TestRegister06_NextOfKinIsWholeOrAbsent'
  [m_nameless]='TestRegister07_ANameIsGiven'
  [m_pending]='TestRegister08_ARegisteredMemberIsActiveTheirOwnPersonAndAttributed'
  [m_unattributed]='TestRegister08_ARegisteredMemberIsActiveTheirOwnPersonAndAttributed'
  [m_batch_twice]='TestRegister09_ABatchIsTheSocietysReferenceOnce'
  [c_nok_required]='TestRegister_Control_AMemberWithNoNextOfKin'
)
