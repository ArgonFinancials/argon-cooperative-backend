# Suite 0016 — a withdrawal request has an author: raised by the member or an
# officer for them, on the raiser's own step-up; the server sets its expiry
# from the society's window; within withdrawable when raised; the raiser never
# signs it; a resubmission is for the same member (decided 2026-10-18).
# Sourced by mutate.sh.
SUITE_INCLUDE='^TestRaise'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestRaise'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,test/tier1/0016_interface.sql'
SUITE_MUTDIR='mutants_0016'
SUITE_GREEN_NEEDS='migrations/0016_withdrawal_requests.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  # M0 excludes TestRaise16: its rule is 0007's (approval_status reads the
  # request's own `required`), so a schema with none of 0016's rules already
  # holds it; mutate.sh caught that (PASSED ON BROKEN SCHEMA under M0,
  # 2026-10-19). Proved instead by m_status_reads_society. It lives here, not
  # in 0007's suite, because its world (money, the operator's quorum) needs
  # 0011 and 0014.
  [M0]='TestRaise(0[1-9]|1[0-5])_'
  [m_raiser_optional]='TestRaise01_ARequestNamesItsRaiser'
  [m_step_up_optional]='TestRaise02_ARequestRestsOnTheRaisersStepUp'
  [m_act_anyones]='TestRaise03_TheActIsTheRaisersOwn'
  [m_act_any_kind]='TestRaise13_TheActIsAWithdrawalRequestAct'
  [m_act_any_transaction]='TestRaise14_TheActIsFromThisTransaction'
  [m_act_reused]='TestRaise15_OneStepUpRaisesOnce'
  [m_anyone_raises]='TestRaise04_OnlyTheMemberOrAnOfficerRaises'
  [m_client_sets_expiry]='TestRaise05_TheServerSetsTheExpiry|TestRaise06_TheSocietysWindowSetsTheExpiry'
  [m_expiry_ignores_window]='TestRaise06_TheSocietysWindowSetsTheExpiry'
  [m_raise_beyond_withdrawable]='TestRaise07_ARequestBeyondWithdrawableIsRefusedWithTheFigures'
  [m_raiser_signs_ok]='TestRaise08_TheRaiserNeverSignsIt'
  [m_resubmission_any_member]='TestRaise09_AResubmissionIsForTheSameMember'
  [m_window_zero_ok]='TestRaise10_TheWindowIsAtLeastOneDay'
  [m_window_unlogged]='TestRaise11_AWindowChangeIsLogged'
  [c_officer_cannot_raise]='TestRaise_Control_AnOfficerRaisesForAMember'
  [m_unreachable_raised]='TestRaise12_ARequestThatCannotReachItsQuorumIsRefused'
  [m_status_reads_society]='TestRaise16_ALowerQuorumDoesNotApproveARaisedRequest'
  [c_quorum_margin]='TestRaise_Control_AnOfficerRaisesForAMember'
  [c_member_cannot_raise]='TestRaise_Control_AMemberRaisesTheirOwn'
  [c_no_resubmission]='TestRaise_Control_AResubmissionForTheSameMember'
)
