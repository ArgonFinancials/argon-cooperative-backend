# Suite 0024 — a first device by enrolment code (decided 2026-11-06). M0 is
# the code's table and functions with no rules.
SUITE_INCLUDE='^TestEnrol'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestEnrol'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,test/tier1/0024_interface.sql'
SUITE_MUTDIR='mutants_0024'
SUITE_GREEN_NEEDS='migrations/0024_enrolment_codes.up.sql'   # written after the red half
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  # 17 (tries never counted as sign-ins) holds on M0 by doing nothing; its
  # proof is its own mutant, a redemption that counts them.
  [M0]='TestEnrol0[1-9]|TestEnrol1[0-6]|TestEnrol18'
  [c_officer_needs_chairman]='TestEnrol_Control_AnOfficerIssuesOnTheirStepUp'
  [c_operator_never]='TestEnrol_Control_TheOperatorIssuesAFoundingOfficersCode'
  [c_void_at_four]='TestEnrol_Control_FourWrongTriesLeaveItLive'
  [m_act_any_member]='TestEnrol06_ACodeRestsOnItsOwnAct'
  [m_anyone_issues]='TestEnrol08_OnlyACurrentOfficerIssues'
  [m_auditor_issues]='TestEnrol08_OnlyACurrentOfficerIssues'
  [m_born_used_ok]='TestEnrol03_ACodeIsBornUntriedUnusedAndLive'
  [m_client_sets_life]='TestEnrol02_TheServerSetsThirtyDays'
  [m_code_for_anyone]='TestEnrol04_ACodeIsForTheMembersOwnPerson'
  [m_deletable]='TestEnrol18_TheRecordIsAppendOnly'
  [m_digest_any]='TestEnrol01_ACodeIsStoredAsADigest'
  [m_digest_mutable]='TestEnrol18_TheRecordIsAppendOnly'
  [m_issuer_optional]='TestEnrol05_ACodeNamesOneIssuer'
  [m_life_mutable]='TestEnrol18_TheRecordIsAppendOnly'
  [m_never_expires]='TestEnrol16_ACodeExpiresAfterThirtyDays'
  [m_old_slip_works]='TestEnrol12_AReplacedSlipStopsWorking'
  [m_operator_for_anyone]='TestEnrol09_TheOperatorIssuesOnlyToOfficers'
  [m_operator_login_optional]='TestEnrol10_TheOperatorNamesThemselvesTheLoginAndWhy'
  [m_operator_reason_optional]='TestEnrol10_TheOperatorNamesThemselvesTheLoginAndWhy'
  [m_operator_unnamed]='TestEnrol10_TheOperatorNamesThemselvesTheLoginAndWhy'
  [m_reissue_keeps_live]='TestEnrol11_AReissueVoidsTheLiveCodeAndIsAttributable'
  [m_reissue_unchained]='TestEnrol11_AReissueVoidsTheLiveCodeAndIsAttributable'
  [m_reuse_ok]='TestEnrol14_ACodeIsSingleUse'
  [m_self_issue_ok]='TestEnrol07_NobodyIssuesTheirOwn'
  [m_tries_count_as_sign_ins]='TestEnrol17_WrongTriesNeverCountAgainstThePinLockout'
  [m_tries_unlimited]='TestEnrol15_FiveWrongTriesVoidIt'
  [m_unuse_ok]='TestEnrol18_TheRecordIsAppendOnly'
)
