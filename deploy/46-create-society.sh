#!/usr/bin/env bash
# The operator founds a society, on its founding resolution, and says who.
# (0023's operator_create_society; LEDGER_CONTRACT.md §12. Tests:
# test/ops/create_society_test.sh; the database's rules: suite 0023.)
#
#   46-create-society.sh --code CODE --name "Registered name" \
#       --registration "LSCS/2019/0412" --directorate Lagos \
#       --withdrawal-quorum N --write-off-quorum N --signature-count N \
#       --operator "Your Name" --reason "why" \
#       --resolution "minutes reference" --resolution-date YYYY-MM-DD [--confirm]
#   (--db NAME, default ARGON_DB from the env file)
#
# Every value is required: the founding resolution sets all three quorums, and
# none has a default. The society starts as a prospect, on the standard chart,
# with a head office. Without --confirm it shows the founding and founds
# nothing (exit 3). The server login is recorded by the script, as 45 records
# it; a caller cannot supply one. The database's refusal is printed as written.
#
# THE BACKUP GATE IS HERE, AND ONLY HERE (ruled 2026-11-06): no society is
# founded while BACKUP_STORE is not s3://, or while the store's newest passing
# drill is more than 8 days old. Checked before the database is touched.
# NOT IN THE DATABASE, ON PURPOSE, and adding it there would not strengthen
# this: the database could only know through a row someone writes, and the
# owner can write that row by hand. A check that can be satisfied by asserting
# the thing it checks is not a check. The 8 days are fixed here, not read from
# the env file: DRILL_MAX_AGE_D there tunes the freshness ALERT, and must never
# loosen what a real society's records wait for.
source "$(dirname "$0")/lib.sh"
set +E
GATE_DAYS=8

usage() { sed -n '5,11p' "$0" | sed 's/^# \{0,1\}//' >&2; }

declare -A v=()
flags="code name registration directorate withdrawal-quorum write-off-quorum signature-count operator reason resolution resolution-date"
confirm=0 db=$ARGON_DB
while (($#)); do
  case $1 in
    --confirm) confirm=1; shift ;;
    --db)      db=${2-}; shift 2 ;;
    --login)   die "--login is refused: the script records the server login itself (user@host, and who ran sudo). A login the caller supplies is not a record of anything." ;;
    -h|--help) usage; exit 1 ;;
    --*)       f=${1#--}; [[ " $flags " == *" $f "* ]] || { usage; die "unknown argument '$1'"; }
               v[$f]=${2-}; shift 2 ;;
    *)         usage; die "unknown argument '$1'" ;;
  esac
done

# ---- the backup gate: before anything else, the database included ------------
[[ ${BACKUP_STORE:-} == s3://* ]] ||
  die "REFUSED: no society is founded while its backups would stay on this instance. BACKUP_STORE is '${BACKUP_STORE:-}', not s3:// (LEDGER_CONTRACT.md §12, 'Reachable is not ready')."
drill=$(store_newest_drill)
[[ -n $drill ]] || die "REFUSED: no passing drill recorded in $BACKUP_STORE/drills. Run 60-restore.sh --drill, and found the society once it passes."
age=$(age_of "$drill")
((age <= GATE_DAYS * 86400)) ||
  die "REFUSED: the newest passing drill is $((age / 86400))d old (limit ${GATE_DAYS}d): $drill. A drill that old does not say today's backups restore. Run 60-restore.sh --drill."
ok "backups are off the instance, and the newest passing drill $((age / 86400))d old: $drill"

# ---- every value, required -----------------------------------------------------
blank() { [[ -z ${1//[[:space:]]/} ]]; }
for f in $flags; do blank "${v[$f]-}" && die "--$f is required: a founding states every value (no quorum has a default)"; done
for f in withdrawal-quorum write-off-quorum signature-count; do
  [[ ${v[$f]} =~ ^[0-9]+$ ]] || die "--$f '${v[$f]}' is not a whole number"
done
[[ ${v[resolution-date]} =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "--resolution-date '${v[resolution-date]}' is not YYYY-MM-DD"
[[ -n $db ]] || die "--db is empty and ARGON_DB is not set"

# The server login: as 45 records it, and labelled the same way (45 says why).
login="$(id -un)@$(hostname) [login, verified by id -un]"
[[ -n ${SUDO_USER:-} ]] && login+="; sudo by $SUDO_USER [claimed: SUDO_USER from the environment, not verified]"

cat >&2 <<EOF

Founding a society
  code               ${v[code]}
  registered name    ${v[name]}
  registration       ${v[registration]}  (${v[directorate]} state directorate)
  quorums            withdrawals ${v[withdrawal-quorum]}, write-offs ${v[write-off-quorum]}, other governed acts ${v[signature-count]}
  on the resolution  ${v[resolution]}, ${v[resolution-date]}
  by                 ${v[operator]}  [$login]
  why                ${v[reason]}
  starts as          a prospect, on the standard chart, with a head office
EOF
((confirm)) || { info "nothing founded: run again with --confirm"; exit 3; }

# Values only ever through -v, never spliced into SQL text.
id=$(pg_as argon_migrator "$db" -q -At -v ON_ERROR_STOP=1 \
  -v code="${v[code]}" -v name="${v[name]}" -v reg="${v[registration]}" -v dir="${v[directorate]}" \
  -v wq="${v[withdrawal-quorum]}" -v woq="${v[write-off-quorum]}" -v sc="${v[signature-count]}" \
  -v op="${v[operator]}" -v login="$login" -v ref="${v[resolution]}" -v rdate="${v[resolution-date]}" -v why="${v[reason]}" \
  -f - <<'SQL'
SELECT operator_create_society(:'code', :'name', :'reg', :'dir', :'wq'::int, :'woq'::int, :'sc'::int,
                               :'op', :'login', :'ref', :'rdate'::date, :'why');
SQL
) || die "the database refused the founding (its reason is above): nothing was founded"
ok "founded ${v[code]} ($id): a prospect, on the standard chart. Next: its first officers and their enrolment codes."
