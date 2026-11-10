# Suite vfd — the VFD client (stage 3, 2026-11-10), tested against a fake VFD
# inside the test: no network, no real token. Sourced by mutate.sh. The breaks
# are in the reference client (vfdfake_client_test.go), selected by
# ARGON_TEST_VFD_FAKE. Run with --svc.
SUITE_INCLUDE='^TestVfdClient'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestVfdClient'
SVC_RUN='^TestVfdClient'
SVC_ENV='ARGON_TEST_VFD_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql'
SUITE_MUTDIR='mutants_vfd_none'
SUITE_GREEN_NEEDS='internal/vfd/client.go'      # the real client, written after the tests
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [no_timeout]='TestVfdClient05_NoAnswerInTimeIsUnknownAndPrompt'
  [status_any_2xx]='TestVfdClient03_A200ThatRefusesIsARefusal'
  [http_5xx_read]='TestVfdClient04_A5xxIsUnknownWhateverItsBodySays'
  [timeout_as_refused]='TestVfdClient05_NoAnswerInTimeIsUnknownAndPrompt'
  [timeout_as_not_reached]='TestVfdClient05_NoAnswerInTimeIsUnknownAndPrompt'
  [dead_as_unknown]='TestVfdClient06_NoConnectionIsNotReached'
  [unreadable_as_refused]='TestVfdClient07_AnUnreadable200IsUnknown'
  [reference_made_here]='TestVfdClient01_AnIntraBankTransferIsSentAsVFDExpectsAndAccepted'
  [zero_amount_sent]='TestVfdClient08_NoReferenceNothingSent'
  [intra_as_inter]='TestVfdClient01_AnIntraBankTransferIsSentAsVFDExpectsAndAccepted'
  [signature_reversed]='TestVfdClient01_AnIntraBankTransferIsSentAsVFDExpectsAndAccepted'
  [amount_float_in]='TestVfdClient09_ASucceededTransferWithItsExactAmount'
  [ninety_nine_success]='TestVfdClient10_NinetyNineIsNotSuccess'
  [notfound_as_pending]='TestVfdClient11_NotFoundIsNotFound'
  [unreadable_as_notfound]='TestVfdClient12_ARequeryThatCannotBeReadIsUnknownNeverNotFound'
  [names_swapped]='TestVfdClient13_AnAccountOpenedIsTheNumberVFDGave'
  [number_invented]='TestVfdClient14_NoNumberFromVFDNoAccount'
  [number_float_taken]='TestVfdClient14_NoNumberFromVFDNoAccount'
  [open_fallback]='TestVfdClient15_AnAmbiguousOpeningIsUnknownAndNotRetried'
  [statement_empty]='TestVfdClient18_NoStatementEndpointIsKnownAndTheClientSaysSo'
  [token_logged]='TestVfdClient19_TheTokenBVNAndBirthDateAreNeverLogged'
  [token_optional]='TestVfdClient20_NoTokenOrNoURLNoClient'
  [enquiry_as_inter]='TestVfdClient17_NameEnquiryFindsTheParty'
  [found_ignoring_status]='TestVfdClient17_NameEnquiryFindsTheParty'
  [inter_no_session]='TestVfdClient02_AnInterBankTransferCarriesTheSessionAndBVN'
  [exists_as_refused]='TestVfdClient16_AnExistingClientIsExistsAndOpenAnotherNamesTheAccount'
)
