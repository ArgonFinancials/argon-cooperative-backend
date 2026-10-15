# Suite 0015 — nobody authorises, signs or attests to a movement they benefit
# from; the authoriser never records; a contribution is recorded once
# (decided 2026-10-14). Sourced by mutate.sh.
SUITE_INCLUDE='^TestBenefit'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestBenefit'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,test/tier1/0015_interface.sql'
SUITE_MUTDIR='mutants_0015'
SUITE_GREEN_NEEDS='migrations/0015_benefit_and_duties.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestBenefit[0-9]'
  [m_self_hold_ok]='TestBenefit01_NobodyAuthorisesTheirOwnPayout'
  [m_self_payout_record_ok]='TestBenefit02_NobodyRecordsTheirOwnPayout|TestBenefit03_NobodyRecordsTheirOwnUnheldPayout'
  [m_self_contribution_ok]='TestBenefit04_NobodyRecordsTheirOwnContribution'
  [m_authoriser_records_ok]='TestBenefit05_TheAuthoriserNeverRecordsThePayout'
  [m_reference_optional]='TestBenefit06_ASingleContributionNamesItsClientReference'
  [m_reference_reusable]='TestBenefit07_AClientReferenceIsRecordedOnce'
  [m_self_release_ok]='TestBenefit08_NobodyReleasesTheirOwnHold'
  [c_any_member_conflicted]='TestBenefit_Control_TheCorrectFormIsAccepted|TestBenefit_Control_AnOfficerRecordsAnotherMembersContribution|TestBenefit_Control_AnotherSignatoryReleasesTheHold'
  [c_reference_global]='TestBenefit_Control_AnotherSocietyMayUseTheSameReference'
  [c_authoriser_must_record]='TestBenefit_Control_AnotherOfficerRecordsTheHeldPayout'
)
