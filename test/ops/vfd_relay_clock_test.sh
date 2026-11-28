#!/usr/bin/env bash
# 67-check-vfd-relay-clock.sh: the VFD relay's clock drifting is told at 10
# seconds, long before 60 stops every call. The relay is a small local server
# that answers as the relay does to an unsigned call (401, X-Argon-Proxy, and
# its clock SKEW seconds behind).
#
#   bash test/ops/vfd_relay_clock_test.sh
# VFD_CLOCK_SCRIPT (a sabotaged copy) overrides.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e
SCRIPT="${VFD_CLOCK_SCRIPT:-$DEPLOY_DIR/67-check-vfd-relay-clock.sh}"
python3 -c '' 2>/dev/null && py=python3 || py=python
W=$(mktemp -d)
cat >"$W/relay.py" <<'PY'
import http.server, sys, time
skew, header = int(sys.argv[2]), sys.argv[3] == "1"
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(401)
        self.send_header("X-Argon-Proxy", "bad_signature")
        if header:
            self.send_header("X-Argon-Proxy-Time", str(int(time.time()) - skew))
        self.end_headers()
        self.wfile.write(b'{"proxy":"argon-vfd-proxy","outcome":"bad_signature"}')
    def log_message(self, *a): pass
s = http.server.HTTPServer(("127.0.0.1", 0), H)
open(sys.argv[1], "w").write(str(s.server_address[1]))
s.serve_forever()
PY
cat >"$W/alert" <<EOF
#!/usr/bin/env bash
{ echo "TITLE: \$1"; cat; } >>"$W/alerts"
EOF
chmod +x "$W/alert"
PID=
stop_relay() { [[ -n $PID ]] && kill "$PID" 2>/dev/null; wait "$PID" 2>/dev/null; PID=; }
trap 'stop_relay; rm -rf "$W"' EXIT
# relay SKEW HEADER: a relay SKEW seconds behind; HEADER 0 sends no clock.
relay() {
  stop_relay; rm -f "$W/port"
  "$py" "$W/relay.py" "$W/port" "$1" "$2" & PID=$!
  for _ in $(seq 50); do [[ -s $W/port ]] && break; sleep 0.1; done
  [[ -s $W/port ]] || die "the test relay did not start"
  URL="http://127.0.0.1:$(cat "$W/port")"
}

T=""; RED=(); P=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; P=$((P + 1)); }
run() { rm -f "$W/alerts"; VFD_CLOCK_ALERT="$W/alert" bash "$SCRIPT" "$@" >"$W/out" 2>&1; }

T="K01 the clocks in step: exit 0, no alert"
relay 0 1; run --url "$URL"; rc=$?
[[ $rc == 0 && ! -f $W/alerts ]] && pass || red "exit $rc, alerts: $(cat "$W/alerts" 2>/dev/null)"

T="K02 the relay 30 seconds behind: an alert saying how far and which way, exit 3"
relay 30 1; run --url "$URL"; rc=$?
[[ $rc == 3 ]] && grep -qE "relay's clock is 3[01] seconds behind" "$W/alerts" && pass ||
  red "exit $rc: $(tr '\n' ' ' <"$W/alerts" 2>/dev/null | cut -c1-200)"

T="K03 the relay 30 seconds ahead: alerted too (drift either way stops calls)"
relay -30 1; run --url "$URL"; rc=$?
[[ $rc == 3 ]] && grep -qE "2[89] seconds ahead|3[01] seconds ahead" "$W/alerts" && pass ||
  red "exit $rc: $(tr '\n' ' ' <"$W/alerts" 2>/dev/null | cut -c1-200)"

T="K04 13 seconds apart: alerted (the line is 10, not 60)"
relay 13 1; run --url "$URL"; rc=$?
[[ $rc == 3 && -f $W/alerts ]] && pass || red "exit $rc: 13 seconds of drift said nothing"

T="K05 6 seconds apart: not alerted (ordinary drift is not a page)"
relay 6 1; run --url "$URL"; rc=$?
[[ $rc == 0 && ! -f $W/alerts ]] && pass || red "exit $rc, alerts: $(cat "$W/alerts" 2>/dev/null)"

T="K06 an answer without the relay's clock: 'cannot read', never 'in step'"
relay 0 0; run --url "$URL"; rc=$?
[[ $rc == 3 ]] && grep -q "cannot read" "$W/alerts" && pass || red "exit $rc"

T="K07 no answer at all: 'cannot read', exit 3"
stop_relay; run --url "$URL"; rc=$?
[[ $rc == 3 ]] && grep -q "cannot read" "$W/alerts" && pass || red "exit $rc: $(tail -2 "$W/out")"

T="K08 --url is required, never defaulted"
VFD_BASE_URL=http://127.0.0.1:9 run; rc=$?
[[ $rc != 0 ]] && grep -q -- "--url is required" "$W/out" && pass || red "exit $rc"

echo
declared=$(grep -cE '^T="K[0-9]+ ' "$0")
if ((${#RED[@]})); then echo "vfd relay clock: ${#RED[@]} RED of $declared: ${RED[*]}"; exit 1; fi
((P == declared)) || { echo "vfd relay clock: $P passed of $declared declared"; exit 1; }
echo "vfd relay clock: all $declared green"
