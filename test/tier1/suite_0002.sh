# Suite 0002 — ledger edges, signatures, periods, adjustments.
# Sourced by mutate.sh. Paths are relative to test/tier1 and the repo root.
SUITE_INCLUDE='^Test'              # tests in this suite …
# … except the other suites. This suite is defined by EXCLUSION, so every new
# suite's prefix must be added here, or it is silently swept into 0002 and run
# against schemas it was never written for (it happened: 53/82).
SUITE_EXCLUDE='^(TestAuth|TestRole|TestApi|TestProof|TestStepUp|TestLineSociety|TestLoan|TestApproval|TestWriteOff|TestSignStep|TestMoney|TestPayout|TestVAT|TestOperator|TestBenefit|TestRaise)'
SUITE_SKIP='^(TestAuth|TestRole|TestApi|TestProof|TestStepUp|TestLineSociety|TestLoan|TestApproval|TestWriteOff|TestSignStep|TestMoney|TestPayout|TestVAT|TestOperator|TestBenefit|TestRaise)'   # passed to -test.skip
SUITE_RUN=''
SUITE_BASE='migrations/0001_init.up.sql,test/tier1/0002_interface.sql'
SUITE_MUTDIR='mutants'
SUITE_GREEN_NEEDS='migrations/0002_tier1.up.sql'
CONTROL_RE='Control|^TestM2_|^TestA1_'  # must PASS on M0
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestG1_BalancedPair|TestG1_CallerCannot|TestG1_OlderTransaction|TestS1_|TestS2_|TestS3_|TestV4_|TestG4_Relinking|TestU1_OnePerson|TestU2_SuppliedUserMust|TestU3_|TestU4_|TestU5_|TestG3_AuditorCannot.*|TestG4_(AuditorThen|SignatoryThen|AuditorAndSignatory|Extending)|TestV1_|TestV2_|TestM1_|TestM3_|TestM4_|TestP1_|TestP3_.*|TestP4_PeriodsInOne|TestA2_|TestA3_|TestA4_|TestA5_'
  [g1_posted_at_server_only]='TestG1_OlderTransactionCannotAppendToNewerEntry'
  [s_any_appointment]='TestS1_NonSigningPostCannotSign'
  [s_ignores_term]='TestS2_'
  [s_trusts_signed_at]='TestS3_'
  [s_chairman_only]='TestS_Control_CurrentSignatorySigns'
  [v_batch_as_of_kept]='TestV4_BatchesCarryNoAsOfColumn'
  [g1_xmin_ignores_savepoints]='TestG1_Control_EntryCreatedInsideSavepoint'
  [g1_latest_entry_only]='TestG1_Control_BatchOfEntriesInOneTransaction'
  [u_keyed_on_member]='TestU1_OnePersonTwoMemberRecordsCannotSignTwice'
  [u_trusts_supplied_user]='TestU1_OnePerson|TestU2_SuppliedUserMust|TestU3_|TestU4_'
  [u_one_signature_per_request]='TestU1_Control_DifferentPeopleSignSameRequest'
  [u_one_signature_per_person_ever]='TestU1_Control_SamePersonSignsDifferentRequests'
  [u_rejects_supplied_user]='TestU2_Control_SuppliedMatchingUserIsAccepted'
  [u_synced_not_frozen]='TestU4_SignatureUserIsFrozenAtSigning'
  [g3_label_only]='TestG3_AuditorCannotSignUnderAnotherLabel|TestG3_AuditorCannotSignThroughSecondMemberRecord'
  [g3_member_level]='TestG3_AuditorCannotSignThroughSecondMemberRecord'
  [g3_ignores_term]='TestG3_Control_FormerAuditorMaySign'
  [g4_member_level]='TestG4_AuditorAndSignatoryAcrossTwoMemberRecords'
  [g4_signatory_insert_only]='TestG4_SignatoryThenAuditorSameMember|TestG4_ExtendingAuditorTermIntoSignatoryTerm'
  [g4_ignores_term]='TestG4_Control_ConsecutiveTerms'
  [g4_any_post]='TestG4_Control_AuditorMayHoldNonSigningPost'
  [v_filled_with_posting_date]='TestV2_UnknownValueDateStaysNull'
  [v_null_for_any_kind]='TestV3_OnlyOpeningBalanceMayHaveUnknownDate'
  [v_opening_balance_always_null]='TestV_Control_OpeningBalanceWithKnownAsOf'
  [m_entry_column_kept]='TestM1_EntriesCarryNoMemberColumn'
  [m_no_fk]='TestM3_LineMemberMustExist|TestM4_LineMemberMustBelongToEntrysSociety'
  [m_no_society_check]='TestM4_LineMemberMustBelongToEntrysSociety'
  [m_one_member_per_entry]='TestM2_MemberToMemberTransferInOneEntry'
  [m_member_required]='TestM_Control_LinesWithoutMember'
  [p_checks_posted_at]='TestP1_PostingIntoClosedPeriodIsRefused'
  [p_close_not_final]='TestP3_ClosedPeriodCannotBeReopened|TestP3_ClosedPeriodBoundariesCannotMove'
  [p_only_reopen_refused]='TestP3_ClosedPeriodBoundariesCannotMove'
  [p_ignores_closed]='TestP2_Control_PostingIntoOpenPeriod'
  [p_overlap_across_societies]='TestP4_Control_SameDatesInAnotherSociety'
  [a_fk_only]='TestA2_|TestA4_|TestA5_'
  [a_no_society_check]='TestA5_AdjustmentMustBeInSameSociety'
  [a_allows_both]='TestA4_CorrectionIsReversalOrAdjustmentNotBoth'
  [a_period_rule_on_original]='TestA1_AdjustmentOfClosedPeriodEntryPostsInOpenPeriod'
)
