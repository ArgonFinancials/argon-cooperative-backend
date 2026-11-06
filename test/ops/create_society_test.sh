#!/usr/bin/env bash
# Tests for deploy/46-create-society.sh, the operator's way of founding a
# society (0023's function; LEDGER_CONTRACT.md §12). Written BEFORE the script,
# 2026-11-06.
#
# The database enforces the founding's rules (suite 0023). These tests are
# about what the SCRIPT adds:
#   G*  THE BACKUP GATE, which is the script's alone: no society is founded
#       while BACKUP_STORE is not s3://, or while the store's newest passing
#       drill is more than 8 days old (or there is none). Checked BEFORE the
#       database is touched, and not loosened by anything in the env file.
#   S*  what the script asks for, records and prints, as 45 does: every value
#       required (no quorum has a default), the server login recorded by the
#       script, nothing applied without --confirm, the database's refusal
#       printed as written, the operator's words verbatim.
# Every refusal is also checked to have founded nothing.
#
#   bash test/ops/create_society_test.sh [--keep]
#   CREATE_SCRIPT=/path/to/copy   runs against another copy (sabotage runs)
#
# S3 is a fake `aws` on PATH, serving s3://BUCKET/... from a local folder, in
# the listing format the real one prints. Runs against a scratch
# argon_verify_found_<ts>_<pid> built from the migrations, never a real database.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e

SCRIPT="${CREATE_SCRIPT:-$DEPLOY_DIR/46-create-society.sh}"
keep=0; [[ ${1:-} == --keep ]] && keep=1
W=$(mktemp -d)
db="argon_verify_found_$(date -u +%Y%m%dt%H%M%S)_$$"
create_scratch_db "$db" >/dev/null || die "cannot create $db"
((keep)) || trap 'rm -rf "$W"; drop_scratch_db "$db" >/dev/null 2>&1 || true' EXIT
run_migrate "$db" up >/dev/null 2>&1 || die "migrations did not apply to $db"
apply_grants "$db" || die "grants did not apply to $db"
sql() { pg_as argon_migrator "$db" -At -q -c "$1" </dev/null; }
[[ $(sql "SELECT (to_regclass('public.standard_chart') IS NOT NULL)::int") == 1 ]] ||
  die "schema before 0023: nothing for the script to call"

T=""; RED=(); N=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; }

# ---- the fake S3 ---------------------------------------------------------------
mkdir -p "$W/bin" "$W/s3"
cat >"$W/bin/aws" <<EOF
#!/usr/bin/env bash
# aws s3 ls --recursive s3://BUCKET/PREFIX/ : "DATE TIME SIZE KEY", KEY from the bucket's root.
[[ \$1 == s3 && \$2 == ls ]] || { echo "fake aws: only 's3 ls' (\$*)" >&2; exit 2; }
url=\${@: -1}; rest=\${url#s3://}; bucket=\${rest%%/*}
[[ -d "$W/s3/\$bucket" ]] || { echo "An error occurred (NoSuchBucket)" >&2; exit 1; }
cd "$W/s3/\$bucket" && find "\${rest#*/}" -type f 2>/dev/null | sort | while read -r k; do echo "2026-11-06 09:07:11        120 \$k"; done
exit 0
EOF
chmod +x "$W/bin/aws"
BUCKET=argon-test-backups
# drill DAYS_AGO: a passing drill's record, named as 60-restore.sh names it.
drill() {
  local ts; ts=$(date -u -d "-$1 days" +%Y%m%dT%H%M%SZ)
  local d="$W/s3/$BUCKET/prod/drills/${ts:0:4}/${ts:4:2}/${ts:6:2}"
  mkdir -p "$d"; printf 'result=PASS\n' >"$d/drill-$ts.ok"
}
nodrills() { rm -rf "$W/s3/$BUCKET/prod/drills"; mkdir -p "$W/s3/$BUCKET/prod"; }

# envf [lines...]: the local env file, then these lines (later lines win when sourced).
envf() { { cat "$ARGON_ENV"; printf '%s\n' "$@"; } >"$W/env"; }

# ---- running it --------------------------------------------------------------
OUT="$W/out"; RC=0
code() { C="FND$$X$((++N))"; }
args() {
  printf '%s\0' --code "$C" --name "Ikeja Teachers' Cooperative Society" --registration "LSCS/2019/0412" --directorate Lagos \
    --withdrawal-quorum 2 --write-off-quorum 3 --signature-count 2 \
    --operator "Adaeze Okafor (Argon operations)" --reason "Onboarding on the founding resolution" \
    --resolution "Founding resolution, inaugural meeting, item 3" --resolution-date 2026-09-30
}
run() {  # run [ARGS...] (after the defaults; a later flag wins)
  RC=0
  local -a a; mapfile -d '' a < <(args)
  # --db always: without it the script falls back to the env file's ARGON_DB,
  # the developer's own database (the first run did exactly that).
  env -u SUDO_USER PATH="$W/bin:$PATH" ARGON_ENV="$W/env" bash "$SCRIPT" "${a[@]}" --db "$db" "$@" >"$OUT" 2>&1 </dev/null || RC=$?
}
founded() { sql "SELECT count(*) FROM societies WHERE code = '$1'"; }

# =============================================================================
# G: THE BACKUP GATE
# =============================================================================
T="G01 a store on this instance (file://) founds nothing, and says why"
code; envf "BACKUP_STORE=file://$W/disk"; mkdir -p "$W/disk/drills/2026/11/06"; : >"$W/disk/drills/2026/11/06/drill-$(date -u +%Y%m%dT%H%M%SZ).ok"
run --confirm
if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then red "rc $RC, founded $(founded "$C"): $(tail -2 "$OUT")"
elif ! grep -q 'not s3://' "$OUT"; then red "refused, but not for the store: $(tail -2 "$OUT")"
else pass; fi

T="G02 S3 with no passing drill founds nothing"
code; envf "BACKUP_STORE=s3://$BUCKET/prod"; nodrills
run --confirm
if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then red "rc $RC, founded $(founded "$C")"
elif ! grep -q 'no passing drill' "$OUT"; then red "refused, but not for the drill: $(tail -2 "$OUT")"
else pass; fi

T="G03 S3 whose newest passing drill is 9 days old founds nothing"
code; nodrills; drill 30; drill 9
run --confirm
if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then red "rc $RC, founded $(founded "$C")"
elif ! grep -qE 'passing drill is 9d old \(limit 8d\)' "$OUT"; then red "refused, but not for the drill's age: $(tail -2 "$OUT")"
else pass; fi

T="G04 the env file cannot loosen the gate: DRILL_MAX_AGE_D=30 there, a 9-day drill still refused"
code; envf "BACKUP_STORE=s3://$BUCKET/prod" "DRILL_MAX_AGE_D=30"
run --confirm
if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then red "the env file's 30 days founded a society on a 9-day-old drill"
elif ! grep -q 'limit 8d' "$OUT"; then red "refused, but not by the 8-day gate: $(tail -2 "$OUT")"
else pass; fi

T="G05 the gate is checked before the database: a file:// store with no database names the store"
code; envf "BACKUP_STORE=file://$W/disk"
RC=0; mapfile -d '' a < <(args)
env -u SUDO_USER PATH="$W/bin:$PATH" ARGON_ENV="$W/env" bash "$SCRIPT" "${a[@]}" --db argon_verify_no_such_db --confirm >"$OUT" 2>&1 </dev/null || RC=$?
if ((RC == 0)); then red "accepted"
elif ! grep -q 'not s3://' "$OUT"; then red "the database was reached first: $(tail -2 "$OUT")"
else pass; fi

T="G06 S3 with a passing drill 2 days old founds the society (the control)"
code; envf "BACKUP_STORE=s3://$BUCKET/prod"; nodrills; drill 2
run --confirm
if ((RC != 0)) || [[ $(founded "$C") != 1 ]]; then red "rc $RC, founded $(founded "$C"): $(tail -3 "$OUT")"
elif ! grep -qE 'newest passing drill 2d old' "$OUT"; then red "founded, without saying which drill let it: $(tail -3 "$OUT")"
else pass; fi

# =============================================================================
# S: WHAT THE SCRIPT ASKS FOR, RECORDS AND PRINTS (the gate open: drill 2d old)
# =============================================================================
T="S01 without --confirm it shows the founding and founds nothing (exit 3)"
code; run
if ((RC != 3)) || [[ $(founded "$C") != 0 ]]; then red "rc $RC, founded $(founded "$C")"
elif ! grep -q "$C" "$OUT"; then red "showed nothing of what it would found"
else pass; fi

T="S02 --login is refused: the script records the login itself"
code; run --login "someone@else" --confirm
if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then red "a caller-supplied login was accepted"; else pass; fi

T="S03 every value is required, each quorum too: no default, refused before the database"
bad=""
for f in --code --name --registration --directorate --withdrawal-quorum --write-off-quorum --signature-count --operator --reason --resolution --resolution-date; do
  code; run "$f" "" --confirm
  if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then bad+=" $f(accepted)"
  elif ! grep -q -- "$f" "$OUT"; then bad+=" $f(refused without naming it)"; fi
done
[[ -z $bad ]] && pass || red "$bad"

T="S04 the server login is recorded by the script in the founding's log"
code; run --confirm
login=$(sql "SELECT server_login FROM society_settings_log l JOIN societies s ON s.id = l.society_id WHERE s.code = '$C' AND setting = 'founded'")
if [[ $login != "$(id -un)@$(hostname) [login, verified by id -un]"* ]]; then red "recorded '$login'"; else pass; fi

T="S05 the database's refusal is printed as written, and founds nothing"
code; run --withdrawal-quorum 1 --confirm
if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then red "a withdrawal quorum of one founded a society"
elif ! grep -q 'unilateral' "$OUT"; then red "refused without the database's why: $(tail -2 "$OUT")"
else pass; fi

T="S06 the operator's words are recorded verbatim, quotes and all"
code; who="Ngozi O'Brien \"Ops\" (Argon operations)"
run --operator "$who" --confirm
got=$(sql "SELECT operator_name FROM society_settings_log l JOIN societies s ON s.id = l.society_id WHERE s.code = '$C' AND setting = 'founded'")
[[ $got == "$who" ]] && pass || red "recorded '$got'"

# Finding 69: a missing --db fell back to the env file's ARGON_DB, the
# developer's own database; every call failed there, and it looked like the
# tests failing, not the target being wrong. No default target: --db or nothing.
# (The env file here names a database that does not exist, so even a script
# that still falls back reaches nothing real.)
T="S07 without --db it refuses, naming --db: it never chooses a database itself"
code; envf "BACKUP_STORE=s3://$BUCKET/prod" "ARGON_DB=argon_verify_no_such_db"
RC=0; mapfile -d '' a < <(args)
env -u SUDO_USER PATH="$W/bin:$PATH" ARGON_ENV="$W/env" bash "$SCRIPT" "${a[@]}" --confirm >"$OUT" 2>&1 </dev/null || RC=$?
if ((RC == 0)) || [[ $(founded "$C") != 0 ]]; then red "founded with no --db"
elif ! grep -q -- '--db is required' "$OUT"; then red "refused, but not for the missing --db: $(tail -2 "$OUT")"
else pass; fi

echo
if ((${#RED[@]})); then echo "RED: ${RED[*]}"; exit 1; fi
echo "all create-society tests pass"
