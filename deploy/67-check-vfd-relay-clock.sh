#!/usr/bin/env bash
# THE SERVER. The VFD relay's clock against this server's (deploy/vfd-proxy,
# 2026-11-28). Every call to the relay is signed with this server's time and
# refused when the two clocks are more than 60 seconds apart; that refusal is
# not VFD's and no money moves, but every VFD call stops. A signed scheme that
# fails silently on drift works until the day it doesn't: this says so at 10
# seconds, long before the 60 that stops anything.
#
#   67-check-vfd-relay-clock.sh --url https://RELAY:PORT
#
# It sends one UNSIGNED request: the relay refuses it, before VFD is asked,
# and its answer carries its clock (X-Argon-Proxy-Time). Nothing is signed,
# no key is read, VFD is never reached.
#
# Within 10 seconds: exit 0. Further apart: one alert saying by how much and
# which way (alert.sh), exit 3. No clock in the answer, or no answer: an alert
# saying the relay's clock cannot be read, exit 3; never "in step".
# 65-check-freshness.sh runs it when the wallet goes through the relay.
# VFD_CLOCK_ALERT (the tests') is a command given the alert's title and its
# text on stdin.
source "$(dirname "$0")/lib.sh"
set +E

url=
while (($#)); do
  case $1 in
    --url) url=${2-}; shift 2 ;;
    *) die "unknown argument '$1' (67-check-vfd-relay-clock.sh --url URL)" ;;
  esac
done
[[ -n $url ]] || die "--url is required: the relay's address (VFD_BASE_URL in /etc/argon/wallet.env). The script never chooses one itself (finding 69)."
: "${VFD_CLOCK_MAX_SKEW:=10}"

alert() {  # alert TITLE: the text on stdin
  if [[ -n ${VFD_CLOCK_ALERT:-} ]]; then "$VFD_CLOCK_ALERT" "$1"
  else bash "$DEPLOY_DIR/alert.sh" raise "$1" -; fi
}

# No answer is not an error that ends this: it is the alert below.
hdr=$(curl -s -m 20 -o /dev/null -D - "${url%/}/account/enquiry" 2>&1 | tr -d '\r') || true
mine=$(date -u +%s)
theirs=$(sed -nE 's/^[Xx]-[Aa]rgon-[Pp]roxy-[Tt]ime: *([0-9]+) *$/\1/p' <<<"$hdr" | head -1)
if [[ -z $theirs ]]; then
  { echo "The VFD relay's clock could not be read at $url: no answer, or an answer without X-Argon-Proxy-Time."
    echo "Nobody can say the two clocks are in step. At 60 seconds apart every VFD call is refused."
    echo; echo "${hdr:-no answer}" | head -5
  } | alert "VFD relay clock: cannot read"
  exit 3
fi
skew=$((mine - theirs))
abs=${skew#-}
if ((abs <= VFD_CLOCK_MAX_SKEW)); then
  ok "the VFD relay's clock is ${skew}s from this server's (alert over ${VFD_CLOCK_MAX_SKEW}s)"
  exit 0
fi
if ((skew > 0)); then way="behind this server's"; else way="ahead of this server's"; fi
echo "  !! the VFD relay's clock is ${abs}s $way" >&2
{ echo "The VFD relay's clock is ${abs} seconds $way (alert over ${VFD_CLOCK_MAX_SKEW}s)."
  echo "At 60 seconds apart every signed call is refused as stale and nothing reaches VFD."
  echo "Check timedatectl and chrony on both servers; the one that is wrong is the one not synchronised."
} | alert "VFD relay clock ${abs}s apart"
exit 3
