#!/usr/bin/env bash
# Tests for going live (2026-10-30): the API on its public name over HTTPS.
# Written BEFORE the templates and scripts they test.
#
#   O*  the origin rule (deploy/nginx/check-origin.sh): ARGON_ALLOWED_ORIGINS
#       is the app's one https origin, under the API's own domain (§12: the
#       session cookie needs one registrable domain), and nothing else.
#   L*  the loopback guard (deploy/75-assert-loopback.sh), fed fake `ss` and
#       `docker` output: 80 and 443 are public only once the API has a public
#       name, and only for nginx; 22 only for sshd.
#   N*  nginx itself, running the RENDERED templates (deploy/nginx/*.conf.in
#       through deploy/nginx/render-public.sh, exactly as 86-install-tls.sh
#       renders them) on high ports, with a self-signed certificate and a stub
#       API that records every request it receives:
#         port 80 serves the certificate check and redirects; it never passes
#         a request to the API and never takes a PIN;
#         443 serves the API with HSTS; unknown names get nothing;
#         sign-in and code checks are rate limited per address, preflights
#         are not counted, and nginx's own refusals carry the app's CORS
#         headers so the app can read them.
#
#   bash test/ops/going_live_test.sh            (needs nginx, openssl, curl, python3)
#   GL_DEPLOY=/path/to/deploy-copy              runs against another copy (sabotage runs)
#   NGINX_BIN=/path/to/nginx                    another nginx binary (never NGINX: nginx reads that itself)
#
# Runs nothing as root and touches nothing outside a temporary directory.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
D="${GL_DEPLOY:-$here/../../deploy}"
D="$(cd "$D" && pwd)"
NGINX_BIN="${NGINX_BIN:-nginx}"
PY=python3; python3 -c '' 2>/dev/null || PY=python

W=$(mktemp -d)
# Long names once, here: nginx on Windows cannot open 8.3 short names (VIRTUA~1),
# and a path that does not exist yet cannot be expanded later.
command -v cygpath >/dev/null && W=$(cygpath -u "$(cygpath -m -l "$W")")
NGX_PID=""; UP_PID=""
# nginx is stopped BEFORE its directory goes: with the directory gone, `-s stop`
# has no pid file, and the first sabotage run leaked an nginx that answered
# for every copy after it.
cleanup() {
  declare -F stop_nginx >/dev/null && stop_nginx
  declare -F stop_upstream >/dev/null && stop_upstream
  rm -rf "$W"
}
trap cleanup EXIT

T=""; RED=()
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; }
# A path nginx can read: on Windows (Git Bash) it wants C:/..., not /c/...
npath() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else echo "$1"; fi; }

echo "deploy dir: $D"

# ---------------------------------------------------------------------------
# O: the origin rule
origin() { bash "$D/nginx/check-origin.sh" "$1" "$2" >"$W/o.out" 2>&1; }

T="O01 the app's https origin under the API's domain is accepted"
if origin "https://coop.example.com" "api-coop.example.com"; then pass; else red "refused: $(cat "$W/o.out")"; fi

T="O02 a second origin is refused: the app's origin and nothing else"
if origin "https://coop.example.com,https://wallet.example.com" "api-coop.example.com"; then red "two origins accepted"; else pass; fi

T="O03 plain http is refused"
if origin "http://coop.example.com" "api-coop.example.com"; then red "http accepted"; else pass; fi

T="O04 an origin on another domain is refused (no SameSite value would send the session cookie)"
if origin "https://coop.example.app" "api-coop.example.com"; then red "another domain accepted"; else pass; fi

T="O05 a domain that only ENDS in the same letters is refused"
if origin "https://evilexample.com" "api-coop.example.com"; then red "suffix-without-dot accepted"; else pass; fi

T="O06 a path, a port, a wildcard or a trailing slash is refused: an origin is scheme://host"
bad=""
for o in "https://coop.example.com/" "https://coop.example.com:8443" "https://*.example.com" "https://coop.example.com/app"; do
  origin "$o" "api-coop.example.com" && bad+=" $o"
done
[[ -z $bad ]] && pass || red "accepted:$bad"

T="O07 empty is refused when the API has a public name: a live API with no app origin serves no browser"
if origin "" "api-coop.example.com"; then red "empty accepted"; else pass; fi

# ---------------------------------------------------------------------------
# R: rendering. A config with a hole is never written.
rvals=(NGX_API_DOMAIN=api-coop.test NGX_APP_ORIGIN=https://coop.test NGX_UPSTREAM=127.0.0.1:1
       NGX_HTTP_LISTEN=127.0.0.1:2 NGX_HTTPS_LISTEN=127.0.0.1:3 NGX_CERT=/c NGX_KEY=/k NGX_ACME_ROOT=/a)

T="R01 a placeholder the renderer does not know is a refusal, not a config with a hole"
printf 'server { server_name @API_DOMAIN@; root @NOT_A_VALUE@; }\n' >"$W/hole.conf.in"
if env "${rvals[@]}" bash "$D/nginx/render-public.sh" "$W/hole.conf.in" >"$W/r.out" 2>&1; then red "rendered: $(head -c 120 "$W/r.out")"; else pass; fi

T="R02 a missing value is a refusal"
if env "${rvals[@]}" NGX_CERT= bash "$D/nginx/render-public.sh" "$D/nginx/argon-api-tls.conf.in" >"$W/r.out" 2>&1; then red "rendered with no certificate"; else pass; fi

# ---------------------------------------------------------------------------
# L: the loopback guard, against fake ss/docker output
mkdir -p "$W/bin"
cat >"$W/bin/ss" <<'EOF'
#!/usr/bin/env bash
cat "$GL_SS"
EOF
cat >"$W/bin/docker" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$W/bin/ss" "$W/bin/docker"
envf() {  # envf [extra lines...] -> an env file for lib.sh as THE SERVER
  { echo "TARGET=docker"; echo "ARGON_HOST=1"; echo "BACKUP_STORE=file://$W/store"; printf '%s\n' "$@"; } >"$W/l.env"
}
SSHD='users:(("sshd",pid=900,fd=3))'
NGX='users:(("nginx",pid=1200,fd=6),("nginx",pid=1201,fd=6))'
PYP='users:(("python3",pid=1300,fd=3))'
loopback() {  # loopback "addr:port proc" ... -> exit status of 75, output in $W/l.out
  : >"$W/ss2.txt"
  for pair in "$@"; do printf 'LISTEN 0 511 %s 0.0.0.0:* %s\n' "${pair% *}" "${pair#* }" >>"$W/ss2.txt"; done
  GL_SS="$W/ss2.txt" PATH="$W/bin:$PATH" ARGON_ENV="$W/l.env" bash "$D/75-assert-loopback.sh" >"$W/l.out" 2>&1
}

T="L01 before going live, 80 beyond loopback is refused"
envf
if loopback "0.0.0.0:22 $SSHD" "0.0.0.0:80 $NGX"; then red "port 80 accepted with no public name"; else pass; fi

T="L02 with a public name, nginx on 80 and 443 and sshd on 22 pass"
envf "ARGON_API_DOMAIN=api-coop.example.com"
if loopback "0.0.0.0:22 $SSHD" "0.0.0.0:80 $NGX" "0.0.0.0:443 $NGX" "127.0.0.1:8080 $PYP"; then pass; else red "refused: $(tail -3 "$W/l.out")"; fi

T="L03 with a public name, something OTHER than nginx on 443 is refused"
if loopback "0.0.0.0:22 $SSHD" "0.0.0.0:443 $PYP"; then red "python on 443 accepted"; else pass; fi

T="L04 with a public name, 8080 beyond loopback is still refused"
if loopback "0.0.0.0:22 $SSHD" "0.0.0.0:443 $NGX" "0.0.0.0:8080 $PYP"; then red "8080 accepted"; else pass; fi

T="L05 nginx on 22 is refused: 22 is sshd's"
if loopback "0.0.0.0:22 $NGX"; then red "nginx on 22 accepted"; else pass; fi

T="L06 a listener whose owner cannot be seen is refused, not waved through"
if loopback "0.0.0.0:443 "; then red "unknown owner accepted"; else pass; fi

T="L07 sshd by socket activation (systemd holding 22) passes"
envf
if loopback "0.0.0.0:22 users:((\"systemd\",pid=1,fd=50))"; then pass; else red "refused: $(tail -3 "$W/l.out")"; fi

# ---------------------------------------------------------------------------
# N: nginx running the rendered templates
command -v "$NGINX_BIN" >/dev/null || { echo "  SKIP N*: no nginx ($NGINX_BIN)"; RED+=("N-SKIPPED"); }
if command -v "$NGINX_BIN" >/dev/null; then
HTTP=18980; HTTPS=18943; UP=18981
API=api-coop.test; APP=https://coop.test
mkdir -p "$W/ngx/conf" "$W/ngx/logs" "$W/ngx/temp" "$W/acme/.well-known/acme-challenge" "$W/tls"
echo "token-for-the-check" >"$W/acme/.well-known/acme-challenge/tok123"
# MSYS2_ARG_CONV_EXCL: Git Bash would rewrite "/CN=..." as a Windows path.
MSYS2_ARG_CONV_EXCL='/CN' openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 \
  -subj "/CN=$API" -addext "subjectAltName=DNS:$API" \
  -keyout "$W/tls/key.pem" -out "$W/tls/cert.pem" >/dev/null 2>&1 || { echo "openssl failed"; exit 1; }

# The stub API: answers 200 JSON, and records every request it receives.
cat >"$W/stub.py" <<'EOF'
import sys, json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
log = sys.argv[2]
class H(BaseHTTPRequestHandler):
    def _do(self):
        n = int(self.headers.get("Content-Length") or 0)
        if n: self.rfile.read(n)
        with open(log, "a") as f: f.write(f"{self.command} {self.path}\n")
        b = json.dumps({"stub": True, "path": self.path}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    do_GET = do_POST = do_OPTIONS = do_HEAD = _do
    def log_message(self, *a): pass
ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
EOF
UPLOG="$W/upstream.log"; : >"$UPLOG"
# The stub, like nginx: never someone else's, and stopped means the port is closed.
up_open() { (exec 3<>"/dev/tcp/127.0.0.1/$UP") 2>/dev/null; }
start_upstream() {
  if up_open; then echo "port $UP already answers: a leftover stub? Refusing to test against it."; return 1; fi
  "$PY" "$W/stub.py" "$UP" "$UPLOG" & UP_PID=$!
  for _ in $(seq 50); do up_open && break; sleep 0.1; done; : >"$UPLOG"
}
stop_upstream() {
  [[ -n $UP_PID ]] || return 0
  [[ -r /proc/$UP_PID/winpid ]] && taskkill //F //T //PID "$(cat "/proc/$UP_PID/winpid")" >/dev/null 2>&1
  kill "$UP_PID" 2>/dev/null
  for _ in $(seq 30); do up_open || break; sleep 0.1; done
  wait "$UP_PID" 2>/dev/null; UP_PID=""
  if up_open; then echo "  !! the stub on port $UP would not stop"; return 1; fi
}

render() {
  NGX_API_DOMAIN=$API NGX_APP_ORIGIN=$APP NGX_UPSTREAM="127.0.0.1:$UP" \
  NGX_HTTP_LISTEN="127.0.0.1:$HTTP" NGX_HTTPS_LISTEN="127.0.0.1:$HTTPS" \
  NGX_CERT="$(npath "$W/tls/cert.pem")" NGX_KEY="$(npath "$W/tls/key.pem")" NGX_ACME_ROOT="$(npath "$W/acme")" \
    bash "$D/nginx/render-public.sh" "$D/nginx/$1"
}
render argon-acme.conf.in >"$W/ngx/conf/acme.conf" 2>"$W/r.err" || { cat "$W/r.err"; echo "  RED  N00 the acme template did not render"; RED+=(N00); }
render argon-api-tls.conf.in >"$W/ngx/conf/tls.conf" 2>"$W/r.err" || { cat "$W/r.err"; echo "  RED  N00 the tls template did not render"; RED+=(N00); }
cat >"$W/ngx/conf/nginx.conf" <<EOF
worker_processes 1;
error_log $(npath "$W/ngx/logs/error.log") warn;
pid $(npath "$W/ngx/logs/nginx.pid");
events { worker_connections 64; }
http {
  access_log off;
  client_body_temp_path $(npath "$W/ngx/temp/body");
  proxy_temp_path $(npath "$W/ngx/temp/proxy");
  fastcgi_temp_path $(npath "$W/ngx/temp/fastcgi");
  uwsgi_temp_path $(npath "$W/ngx/temp/uwsgi");
  scgi_temp_path $(npath "$W/ngx/temp/scgi");
  include $(npath "$W/ngx/conf/acme.conf");
  include $(npath "$W/ngx/conf/tls.conf");
}
EOF
# -e: the error log before the config is read (not the packaged /var/log/nginx,
# which a non-root run cannot open).
ngx() { "$NGINX_BIN" -e "$(npath "$W/ngx/logs/error.log")" -p "$(npath "$W/ngx")/" -c "$(npath "$W/ngx/conf/nginx.conf")" "$@"; }
# Something accepts connections on the test's port (a connection, whatever the answer).
port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$HTTP") 2>/dev/null; }
start_nginx() {
  # Never test someone else's nginx: a leftover from an earlier run answered
  # every request of the next sabotage copies (the first sabotage run).
  if port_open; then echo "port $HTTP already answers: a leftover nginx? Refusing to test it."; return 1; fi
  ngx -t >"$W/t.out" 2>&1 || { cat "$W/t.out"; return 1; }
  ngx -g 'daemon off;' >"$W/n.out" 2>&1 & NGX_PID=$!
  for _ in $(seq 50); do port_open && return 0; sleep 0.1; done
  cat "$W/n.out" "$W/ngx/logs/error.log" 2>/dev/null; return 1
}
# Stopped means the port is closed, not that a signal was sent. On Windows a
# bash `kill` does not reach nginx's worker, so the last resort is the tree.
stop_nginx() {
  [[ -n $NGX_PID ]] || return 0
  ngx -s stop >/dev/null 2>&1
  for _ in $(seq 50); do port_open || break; sleep 0.1; done
  if port_open; then
    if [[ -r /proc/$NGX_PID/winpid ]]; then taskkill //F //T //PID "$(cat "/proc/$NGX_PID/winpid")" >/dev/null 2>&1
    else kill "$NGX_PID" 2>/dev/null; pkill -f -- "-p $(npath "$W/ngx")/" 2>/dev/null; fi
    for _ in $(seq 30); do port_open || break; sleep 0.1; done
  fi
  wait "$NGX_PID" 2>/dev/null; NGX_PID=""
  if port_open; then echo "  !! nginx on port $HTTP would not stop"; return 1; fi
}
restart() { stop_nginx; start_nginx; }

H80()  { curl -s -o "$W/body" -D "$W/hdr" -w '%{http_code}' --resolve "$API:$HTTP:127.0.0.1" "$@"; }
H443() { curl -s -o "$W/body" -D "$W/hdr" -w '%{http_code}' --cacert "$W/tls/cert.pem" --resolve "$API:$HTTPS:127.0.0.1" "$@"; }
hdr() { tr -d '\r' <"$W/hdr" | awk -v k="$1" 'BEGIN{IGNORECASE=1} tolower($0) ~ "^"tolower(k)":" {sub(/^[^:]*: */,""); print; exit}'; }
up_saw() { grep -c "$1" "$UPLOG" 2>/dev/null || true; }

if ! start_upstream || ! start_nginx; then echo "  RED  N00 the stub API or nginx (on the rendered templates) would not start"; RED+=(N00)
else

T="N01 port 80 serves the certificate check's file"
c=$(H80 "http://$API:$HTTP/.well-known/acme-challenge/tok123")
[[ $c == 200 && $(cat "$W/body") == token-for-the-check ]] && pass || red "status $c, body '$(head -c 80 "$W/body")'"

T="N02 port 80 redirects a GET to https on the API's own name"
c=$(H80 "http://$API:$HTTP/healthz?x=1")
loc=$(hdr location)
[[ $c == 301 && $loc == "https://$API/healthz?x=1" ]] && pass || red "status $c, location '$loc'"

T="N03 port 80 never passes a sign-in to the API: POST is refused with tls_required"
: >"$UPLOG"
c=$(H80 -X POST -H 'Content-Type: application/json' -d '{"credential":"x","pin":"493817"}' "http://$API:$HTTP/auth/sign-in")
if [[ $c != 403 ]]; then red "status $c"
elif ! grep -q tls_required "$W/body"; then red "body '$(head -c 120 "$W/body")'"
elif [[ -s $UPLOG ]]; then red "the API received: $(head -3 "$UPLOG")"
else pass; fi

T="N04 port 80 answers nothing for a name that is not the API's"
c=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: wallet.example.com" "http://127.0.0.1:$HTTP/healthz"); rc=$?
[[ $c == 000 ]] && pass || red "status $c (curl exit $rc)"

T="N05 443 serves the API, with HSTS"
: >"$UPLOG"
c=$(H443 "https://$API:$HTTPS/healthz")
hsts=$(hdr strict-transport-security)
if [[ $c != 200 ]]; then red "status $c"
elif [[ $(up_saw "GET /healthz") != 1 ]]; then red "the API did not receive it"
elif [[ ! $hsts =~ max-age=([0-9]+) ]] || ((BASH_REMATCH[1] < 31536000)); then red "Strict-Transport-Security '$hsts'"
else pass; fi

T="N06 443 refuses the handshake for a name that is not the API's"
out=$(echo | openssl s_client -connect "127.0.0.1:$HTTPS" -servername wallet.example.com 2>&1 </dev/null)
grep -q 'BEGIN CERTIFICATE\|Server certificate' <<<"$out" && red "a certificate was served to another name" || pass

T="N07 preflights are not counted: 80 OPTIONS, then a sign-in still passes"
restart
for _ in $(seq 80); do H443 -X OPTIONS -H "Origin: $APP" "https://$API:$HTTPS/auth/sign-in" >/dev/null; done
c=$(H443 -X POST -d '{}' "https://$API:$HTTPS/auth/sign-in")
[[ $c == 200 ]] && pass || red "sign-in after preflights: $c"

# N08-N11 are ONE curl process on one connection: 45 sign-ins, then a code
# check, then a sign-in from another origin. Separate processes are slow enough
# on Windows (~0.3 s each) that the bucket refills mid-burst (the first run).
restart
req() {  # req PATH ORIGIN BODY_FILE HDR_FILE: one request's options for the burst
  printf '%s\0' -s -X POST -d '{}' -H "Origin: $2" -o "$3" -D "$4" -w '%{http_code} ' \
    --cacert "$W/tls/cert.pem" --resolve "$API:$HTTPS:127.0.0.1" "https://$API:$HTTPS$1"
}
args=()
for i in $(seq 45); do
  ((i > 1)) && args+=(--next)
  if ((i == 45)); then mapfile -d '' -t r < <(req /auth/sign-in "$APP" "$W/body" "$W/hdr")
  else mapfile -d '' -t r < <(req /auth/sign-in "$APP" /dev/null /dev/null); fi
  args+=("${r[@]}")
done
mapfile -d '' -t r < <(req /auth/codes/verify "$APP" /dev/null /dev/null); args+=(--next "${r[@]}")
mapfile -d '' -t r < <(req /auth/sign-in "https://wallet.example.com" /dev/null "$W/hdr2"); args+=(--next "${r[@]}")
read -ra codes <<<"$(curl "${args[@]}")"

T="N08 sign-in is limited per address: a burst passes, then 429 too_many_requests"
burst="${codes[*]:0:45}"
n200=$(grep -o 200 <<<"$burst" | wc -l); n429=$(grep -o 429 <<<"$burst" | wc -l)
if ((n200 < 29 || n200 > 33)); then red "$n200 passed of 45 (want 31: one, plus a burst of 30): $burst"
elif ((n200 + n429 != 45)); then red "other statuses: $burst"
elif ! grep -q '"too_many_requests"' "$W/body"; then red "last body '$(head -c 160 "$W/body")'"
else pass; fi

T="N09 the 429 carries the app's CORS headers and HSTS, so the app can read it"
acao=$(hdr access-control-allow-origin); acac=$(hdr access-control-allow-credentials); hsts=$(hdr strict-transport-security)
[[ ${codes[44]} == 429 && $acao == "$APP" && $acac == true && -n $hsts ]] && pass || red "status ${codes[44]} ACAO '$acao' ACAC '$acac' HSTS '$hsts'"

T="N10 a 429 to any other origin carries no CORS header"
acao=$(tr -d '\r' <"$W/hdr2" | awk 'tolower($0) ~ /^access-control-allow-origin:/ {sub(/^[^:]*: */,""); print}')
[[ ${codes[46]} == 429 && -z $acao ]] && pass || red "status ${codes[46]}, ACAO '$acao' sent to another origin"

T="N11 code checks share the limit: once sign-in is exhausted, /auth/codes/verify is 429 too"
[[ ${codes[45]} == 429 ]] && pass || red "status ${codes[45]}"

T="N12 the rest of the API is not rate limited: 60 quick reads all pass"
restart
codes=""; for _ in $(seq 60); do codes+="$(H443 "https://$API:$HTTPS/me") "; done
[[ $(grep -o 200 <<<"$codes" | wc -l) == 60 ]] && pass || red "$codes"

T="N13 a request body over 1 MB is refused before the API sees it"
: >"$UPLOG"
head -c 1100000 /dev/zero | tr '\0' 'a' >"$W/big"
c=$(H443 -X POST --data-binary @"$W/big" "https://$API:$HTTPS/societies/x/ledger/batches")
[[ $c == 413 && ! -s $UPLOG ]] && pass || red "status $c; API saw: $(head -1 "$UPLOG")"

T="N14 with the API down, nginx answers 503 service_unavailable with the app's CORS headers"
stop_upstream
c=$(H443 -H "Origin: $APP" "https://$API:$HTTPS/me")
acao=$(hdr access-control-allow-origin)
if [[ $c != 503 ]]; then red "status $c"
elif ! grep -q '"service_unavailable"' "$W/body"; then red "body '$(head -c 160 "$W/body")'"
elif [[ $acao != "$APP" ]]; then red "ACAO '$acao'"
else pass; fi
start_upstream

stop_nginx
fi
fi

# ---------------------------------------------------------------------------
echo
if ((${#RED[@]})); then echo "RED: ${RED[*]}"; exit 1; fi
echo "all green"
