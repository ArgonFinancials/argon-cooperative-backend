#!/usr/bin/env bash
# THE SERVER. Payouts held waiting for VFD (0039; docs/VFD_INTEGRATION.md
# §1.6): VFD answered something other than 00 or 108, so it may have created
# and sent the transfer, and the system never releases it; or a transfer has
# gone a day with no definitive answer. A person must know: they ask VFD
# whether the money reached the member or was reversed to the pool. (On the
# wallet platform a ₦50 transfer has sat at status 91 since 20 October: held
# rightly, told to nobody.)
#
#   66-check-vfd-holds.sh --db NAME
#
# Nothing held: exit 0. Held: one alert listing each (alert.sh), exit 3. The
# held payouts cannot be read: an alert saying so, exit 3; never "nothing
# held". 65-check-freshness.sh runs it on its timer. VFD_HOLDS_ALERT (the
# tests') is a command given the alert's title and its text on stdin.
source "$(dirname "$0")/lib.sh"
set +E

db=
while (($#)); do
  case $1 in
    --db) db=${2-}; shift 2 ;;
    *) die "unknown argument '$1' (66-check-vfd-holds.sh --db NAME)" ;;
  esac
done
[[ -n $db ]] || die "--db is required: name the database (on the server, the one in ARGON_DB). The script never chooses one itself (finding 69)."

alert() {  # alert TITLE: the text on stdin
  if [[ -n ${VFD_HOLDS_ALERT:-} ]]; then "$VFD_HOLDS_ALERT" "$1"
  else bash "$DEPLOY_DIR/alert.sh" raise "$1" -; fi
}

has=$(pg_as argon_app "$db" -At -c "SELECT (to_regclass('public.vfd_pool') IS NOT NULL)::int" </dev/null 2>/dev/null) || has=error
case $has in
  0) ok "no payout can be held for VFD: this database is before 0039"; exit 0 ;;
  1) ;;
  *) echo "The payouts held for VFD could not be read from $db." | alert "payouts held for VFD: cannot read"; exit 3 ;;
esac

rc=0
out=$(pg_as argon_app "$db" -At -c "SELECT detail FROM wallet_exceptions
    WHERE kind = 'transfer_needs_vfd' OR (kind = 'transfer_stuck' AND since < now() - interval '24 hours') ORDER BY since" </dev/null 2>&1) || rc=$?
if ((rc != 0)); then
  { echo "The payouts held for VFD could not be read: nobody can say none are held."; echo; echo "$out"; } | alert "payouts held for VFD: cannot read"
  exit 3
fi
mapfile -t held < <(grep -v '^$' <<<"$out")
if ((${#held[@]} == 0)); then
  ok "no payout held waiting for VFD"
  exit 0
fi
printf '%s\n' "${held[@]}" | sed 's/^/  !! /' >&2
{ echo "A payout is held waiting for VFD. The money stays held and is never released automatically: VFD may have sent it."
  echo "Ask VFD whether each transfer reached the member or was reversed to the pool; then release or record it by hand."
  echo; printf -- '- %s\n' "${held[@]}"
} | alert "payouts held waiting for VFD (${#held[@]})"
exit 3
