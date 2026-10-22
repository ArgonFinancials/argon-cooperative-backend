# Suite cors_api — browser access: a preflight answered before authentication,
# only configured origins, exactly, closed by default (decided 2026-10-22).
# Sourced by mutate.sh. Run with --svc.
SUITE_INCLUDE='^TestApiCors'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiCors'
SVC_RUN='^TestApiCors'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/cors.go'          # the real middleware, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [cors_after_auth]='TestApiCors01_APreflightIsAnsweredWithoutCredentials'
  [cors_headers_missing]='TestApiCors01_APreflightIsAnsweredWithoutCredentials'
  [cors_any_origin]='TestApiCors02_AnUnlistedOriginsPreflightIsRefused|TestApiCors03_OnlyTheExactOrigin|TestApiCors06_AnUnlistedOriginCannotReadACall'
  [cors_wildcard]='TestApiCors02_AnUnlistedOriginsPreflightIsRefused|TestApiCors05_ARealCallFromTheAllowedOriginSucceeds|TestApiCors06_AnUnlistedOriginCannotReadACall'
  [cors_prefix_match]='TestApiCors03_OnlyTheExactOrigin'
  [cors_default_open]='TestApiCors04_NothingConfiguredAllowsNothing'
  [cors_no_vary]='TestApiCors05_ARealCallFromTheAllowedOriginSucceeds'
  [cors_skips_auth]='TestApiCors07_ARealCallStillNeedsASession'
)
