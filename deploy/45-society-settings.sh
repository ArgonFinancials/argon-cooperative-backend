#!/usr/bin/env bash
# The operator sets a society's rules, and says who, on whose decision.
# (0014's functions; LEDGER_CONTRACT.md §10. Tests: test/ops/society_settings_test.sh.)
#
#   45-society-settings.sh SETTING --society CODE --value V \
#       --operator "Your Name" --reason "why" \
#       --resolution "minutes reference" --resolution-date YYYY-MM-DD [--confirm]
#   45-society-settings.sh lifecycle --society CODE --value STATE \
#       --operator "Your Name" --reason "why" [--confirm]
#   45-society-settings.sh charge-series --society CODE --file SERIES.json \
#       --operator "Your Name" --reason "why" [--confirm]
#   45-society-settings.sh show --society CODE
#   (any of them: --db NAME, default ARGON_DB from the env file)
#
# Without --confirm it shows the change and applies nothing (exit 3). The server
# login is recorded by the script ("user@host", and "(sudo by NAME)" under
# sudo); a caller cannot supply one. The database enforces the rules; a refusal
# is printed as the database wrote it, because the why is the point.
#
# Runs as argon_migrator, which lands in argon_owner: the only role that may
# call the operator's functions. The app never can.
source "$(dirname "$0")/lib.sh"
set +E

SETTINGS="withdrawal-quorum write-off-quorum signature-count account-floor guard-threshold lifecycle charge-series"

# What each setting is, said where it is set.
explain() {
  case $1 in
    withdrawal-quorum) echo "Signatures a withdrawal needs. At least 2: a quorum of one makes withdrawals unilateral." ;;
    write-off-quorum)  echo "Signatures a write-off needs. At least 2: one person must never decide alone that a debt will not be recovered." ;;
    signature-count)   echo "Signatures the society's other governed acts need. At least 2." ;;
    account-floor)     echo "What the society's account keeps back, in kobo. Never negative." ;;
    guard-threshold)   echo "The amount, in kobo, above which the app asks a person to confirm more deliberately. It is INTERFACE FRICTION, NOT AUTHORISATION: it decides what a person is asked to confirm, never who must sign. So it has NO MINIMUM; the quorum rule does not apply to it." ;;
    lifecycle)         echo "Argon's state for this society (prospect, verifying, ... active, dormant, departed). It is Argon's state, not the society's decision about itself: a named operator and a reason, and no society resolution." ;;
    charge-series)     echo "A charge series from a file. Charges are Argon's and the partner's configuration, not a society's decision: no society resolution. Never backdated: today, or a future date announced before it takes effect." ;;
  esac
}

usage() {
  {
    sed -n '4,13p' "$0" | sed 's/^# \{0,1\}//'
    echo
    echo "Settings:"
    for s in $SETTINGS; do printf '  %-18s %s\n' "$s" "$(explain "$s")"; done
  } >&2
}

# ---- arguments ---------------------------------------------------------------
setting=${1:-}; shift || true
[[ $setting == -h || $setting == --help || -z $setting ]] && { usage; exit 1; }
society="" value="" operator="" reason="" ref="" rdate="" file="" confirm=0 db=$ARGON_DB
have_ref=0 have_rdate=0
while (($#)); do
  case $1 in
    --society)         society=${2-};  shift 2 ;;
    --value)           value=${2-};    shift 2 ;;
    --operator)        operator=${2-}; shift 2 ;;
    --reason)          reason=${2-};   shift 2 ;;
    --resolution)      ref=${2-};      have_ref=1;   shift 2 ;;
    --resolution-date) rdate=${2-};    have_rdate=1; shift 2 ;;
    --file)            file=${2-};     shift 2 ;;
    --db)              db=${2-};       shift 2 ;;
    --confirm)         confirm=1;      shift ;;
    --login)  die "--login is refused: the script records the server login itself (user@host, and who ran sudo). A login the caller supplies is not a record of anything." ;;
    *)        usage; die "unknown argument '$1'" ;;
  esac
done

case " $SETTINGS show " in *" $setting "*) ;; *) usage; die "unknown setting '$setting'. The settings: ${SETTINGS// /, }" ;; esac
[[ -n $society ]] || die "--society CODE is required"
[[ -n $db ]] || die "--db is empty and ARGON_DB is not set"

blank() { [[ -z ${1//[[:space:]]/} ]]; }

# psql as argon_migrator; SQL on stdin; values only ever through -v, never
# spliced into SQL text.
q() { pg_as argon_migrator "$db" -q -At "$@" -f -; }

sid=$(q -v code="$society" <<<"SELECT id FROM societies WHERE code = :'code'") || die "cannot read societies in '$db'"
[[ -n $sid ]] || die "no society with code '$society' in '$db'"

if [[ $setting == show ]]; then
  pg_as argon_migrator "$db" -q -v soc="$sid" -f - <<'SQL'
\pset footer off
\echo 'Settings'
SELECT code, registered_name, lifecycle, withdrawal_signatures_required, write_off_signatures_required,
       signatures_required, account_floor_kobo, guard_threshold_kobo
  FROM societies WHERE id = :'soc' \gx
\echo 'Charge series'
SELECT kind, component, effective_from, source FROM charge_schedules
 WHERE society_id = :'soc' ORDER BY kind, component, effective_from;
\echo 'Settings log (append-only)'
SELECT at, setting, coalesce(old_value, 'unset') || ' -> ' || new_value AS change, operator_name, server_login,
       resolution_ref, resolution_date, reason
  FROM society_settings_log WHERE society_id = :'soc' ORDER BY at, id \gx
SQL
  exit
fi

# ---- what every change needs ---------------------------------------------------
blank "$operator" && die "--operator is required: name yourself. Someone with shell access is not attribution."
blank "$reason"   && die "--reason is required: a change to a society's rules says why."

case $setting in
  lifecycle|charge-series)
    if ((have_ref || have_rdate)); then
      if [[ $setting == lifecycle ]]; then
        die "--resolution does not belong here: the lifecycle is Argon's state for the society, not the society's decision about itself. It would not be recorded; remove it."
      fi
      die "--resolution does not belong here: a charge series is Argon's and the partner's configuration, not the society's decision. It would not be recorded; remove it."
    fi ;;
  *)
    blank "$ref"   && die "--resolution is required: the operator carries out the society's decision. Name its resolution (minutes reference) and --resolution-date. Without it, an Argon staff member would be the author of a society's governance."
    blank "$rdate" && die "--resolution-date is required (YYYY-MM-DD): the operator carries out the society's decision, and a resolution has a date."
    [[ $rdate =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "--resolution-date '$rdate' is not YYYY-MM-DD" ;;
esac

# The server login, recorded by the script and never by the caller.
login="$(id -un)@$(hostname)"
[[ -n ${SUDO_USER:-} ]] && login+=" (sudo by $SUDO_USER)"

naira() {  # 100000 -> ₦1,000.00 (100000 kobo)
  local k=$1 sign="" n
  [[ $k == -* ]] && { sign="-"; k=${k#-}; }
  n=$((k / 100))
  while [[ $n =~ ^([0-9]+)([0-9]{3})(,.*)?$ ]]; do n="${BASH_REMATCH[1]},${BASH_REMATCH[2]}${BASH_REMATCH[3]}"; done
  printf '%s₦%s.%02d (%s kobo)' "$sign" "$n" "$((k % 100))" "$1"
}
shownv() {  # how a value is shown
  [[ -z $1 ]] && { echo "unset"; return; }
  case $setting in account-floor|guard-threshold) naira "$1" ;; *) echo "$1" ;; esac
}

# ---- the value -----------------------------------------------------------------
case $setting in
  withdrawal-quorum) fn=operator_set_withdrawal_quorum; colname=withdrawal_signatures_required; cast=int ;;
  write-off-quorum)  fn=operator_set_write_off_quorum;  colname=write_off_signatures_required;  cast=int ;;
  signature-count)   fn=operator_set_signature_count;   colname=signatures_required;            cast=int ;;
  account-floor)     fn=operator_set_account_floor;     colname=account_floor_kobo;             cast=bigint ;;
  guard-threshold)   fn=operator_set_guard_threshold;   colname=guard_threshold_kobo;           cast=bigint ;;
  lifecycle)         fn=operator_set_lifecycle;         colname=lifecycle;                      cast=society_lifecycle ;;
  charge-series)     fn=operator_add_charge_series ;;
esac

py() { if python3 -c '' 2>/dev/null; then python3 "$@"; else python "$@"; fi; }

if [[ $setting == charge-series ]]; then
  [[ -n $file ]] || die "--file SERIES.json is required for a charge series"
  [[ -f $file ]] || die "series file '$file' not found"
  # kind, component, from, source on four lines, then the bands as JSON.
  parsed=$(py -c '
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as e:
    sys.exit("not valid JSON: %s" % e)
missing = [k for k in ("kind", "component", "from", "source", "bands") if k not in d]
if missing:
    sys.exit("missing: " + ", ".join(missing))
if not isinstance(d["bands"], list) or not d["bands"]:
    sys.exit("bands must be a non-empty list")
print(d["kind"]); print(d["component"]); print(d["from"]); print(d["source"]); print(json.dumps(d["bands"]))
' "$file" 2>&1) || die "series file '$file': $parsed"
  parsed=${parsed//$'\r'/}   # Python on Windows ends lines with \r\n
  { read -r kind; read -r component; read -r from; read -r source; read -r bands; } <<<"$parsed"
  current=$(q -v soc="$sid" -v kind="$kind" -v comp="$component" <<<"SELECT coalesce(string_agg(effective_from || ' (' || source || ')', '; ' ORDER BY effective_from), 'none')
      FROM charge_schedules WHERE society_id = :'soc' AND kind::text = :'kind' AND component = :'comp'") || die "cannot read the current series"
  now_shown="series for $kind $component: $current"
  new_shown="$kind $component from $from (source: $source): $bands"
else
  blank "$value" && die "--value is required"
  if [[ $setting == lifecycle ]]; then
    states=$(q <<<"SELECT string_agg(e::text, ' ') FROM unnest(enum_range(NULL::society_lifecycle)) e") || die "cannot read the lifecycle states"
    [[ " $states " == *" $value "* ]] || die "--value '$value' is not a lifecycle state. The states: $states"
  else
    [[ $value =~ ^-?[0-9]+$ ]] || die "--value '$value' must be a whole number$([[ $cast == bigint ]] && echo ' of kobo (₦1 = 100)')"
  fi
  current=$(q -v soc="$sid" <<<"SELECT $colname FROM societies WHERE id = :'soc'") || die "cannot read the current value"
  now_shown=$(shownv "$current")
  new_shown=$(shownv "$value")
fi

# ---- the change, shown -----------------------------------------------------------
{
  echo "Society:    $society ($db)"
  echo "Setting:    $setting"
  echo "            $(explain "$setting")"
  if [[ $setting == charge-series ]]; then
    echo "Now:        $now_shown"
    echo "Adding:     $new_shown"
  else
    echo "Change:     $now_shown → $new_shown"
  fi
  echo "Operator:   $operator"
  echo "Login:      $login"
  [[ $setting == lifecycle || $setting == charge-series ]] || echo "Resolution: $ref, $rdate"
  echo "Reason:     $reason"
} >&2

if ((!confirm)); then
  echo "NOT APPLIED: re-run with --confirm to make this change." >&2
  exit 3
fi

# ---- the change, made ------------------------------------------------------------
case $setting in
  lifecycle)
    sql="SELECT $fn(:'soc', :'val'::society_lifecycle, :'op', :'login', :'why');" ;;
  charge-series)
    sql="SELECT $fn(:'soc', :'kind'::movement_kind, :'comp', :'from'::date, :'src', :'bands'::jsonb, :'op', :'login', :'why');" ;;
  *)
    sql="SELECT $fn(:'soc', :'val'::$cast, :'op', :'login', :'ref', :'rdate'::date, :'why');" ;;
esac
rc=0
out=$(printf '\\set VERBOSITY verbose\n%s\n' "$sql" |
      q -v soc="$sid" -v val="$value" -v op="$operator" -v login="$login" -v ref="$ref" -v rdate="$rdate" -v why="$reason" \
        -v kind="${kind:-}" -v comp="${component:-}" -v from="${from:-}" -v src="${source:-}" -v bands="${bands:-}" 2>&1) || rc=$?

if ((rc != 0)); then
  # psql writes "psql:<stdin>:2: ERROR:  23514: message".
  msg=$(sed -n 's/^\(psql:[^ ]*: \)\{0,1\}ERROR:  \([0-9A-Z]\{5\}: \)\{0,1\}//p' <<<"$out" | head -1)
  rule=$(sed -n 's/^CONSTRAINT NAME:  //p' <<<"$out" | head -1)
  if [[ -n $msg ]]; then
    printf '\nREFUSED by the database%s:\n  %s\n' "${rule:+ ($rule)}" "$msg" >&2
  else
    printf '\nFAILED (psql exit %d):\n%s\n' "$rc" "$out" >&2
  fi
  echo "Nothing was changed." >&2
  exit 1
fi

echo "APPLIED. The settings log now reads:" >&2
q -v soc="$sid" <<<"SELECT '  ' || setting || ': ' || coalesce(old_value, 'unset') || ' -> ' || new_value
                      || E'\n  by ' || operator_name || ' (' || server_login || ') at ' || at
                      || coalesce(E'\n  on ' || resolution_ref || ', ' || resolution_date, '')
                      || E'\n  because ' || reason
                     FROM society_settings_log WHERE society_id = :'soc' ORDER BY at DESC, id DESC LIMIT 1" >&2
