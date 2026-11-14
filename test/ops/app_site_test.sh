#!/usr/bin/env bash
# Tests for the app's site (2026-11-14): the frontend's static export served by
# nginx beside the API, from the RENDERED templates (deploy/nginx/argon-app-*.conf.in
# through render-public.sh, as 88-install-app.sh renders them), on high ports,
# with a self-signed certificate for each name and a stub API.
#
#   Phase 1, the PREVIEW (before DNS moves): the API's two sites and
#   argon-app-preview, as the server has them after 88-install-app.sh --preview.
#     A01 a page, with its headers   A02 a route without .html   A03 an unknown path is the 404 page
#     A04 nothing but GET/HEAD        A05 the certificate check's file
#   Phase 2, LIVE (after --tls): the API's two sites, argon-app-acme and argon-app-tls.
#     A06 port 80 redirects a GET to https on the app's own name, and still serves the check
#     A07 port 80 serves no page in the clear and takes no POST
#     A08 443 serves the app with HSTS and its headers   A09 hashed assets: a year, immutable
#     A10 pages are revalidated (no-cache)                A11 an unknown path: the 404 page, status 404
#     A12 nothing but GET/HEAD on 443                     A13 the API still answers on its own name (SNI)
#     A14 a switched release is served without a reload (needs symlinks: Linux, CI)
#   A15 an app template with its app values empty is a refusal
#
#   bash test/ops/app_site_test.sh            (needs nginx, openssl, curl, python3)
#   GL_DEPLOY=/path/to/deploy-copy            runs against another copy (sabotage runs)
#   NGINX_BIN=/path/to/nginx                  another nginx binary
# Runs nothing as root and touches nothing outside a temporary directory.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
D="${GL_DEPLOY:-$here/../../deploy}"
D="$(cd "$D" && pwd)"
NGINX_BIN="${NGINX_BIN:-nginx}"
PY=python3; python3 -c '' 2>/dev/null || PY=python
W=$(mktemp -d)
command -v cygpath >/dev/null && W=$(cygpath -u "$(cygpath -m -l "$W")")
NGX_PID=""; UP_PID=""
cleanup() {
  declare -F stop_nginx >/dev/null && stop_nginx
  declare -F stop_upstream >/dev/null && stop_upstream
  rm -rf "$W"
}
trap cleanup EXIT
T=""; RED=()
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; }
npath() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else echo "$1"; fi; }
command -v "$NGINX_BIN" >/dev/null || { echo "NOT RUN: no nginx ($NGINX_BIN)"; exit 2; }

HTTP=19980; HTTPS=19943; UP=19981
API=api-coop.test; APPD=coop.test; APP=https://$APPD
mkdir -p "$W/ngx/conf" "$W/ngx/logs" "$W/ngx/temp" "$W/acme/.well-known/acme-challenge" "$W/tls"
echo "token-for-the-check" >"$W/acme/.well-known/acme-challenge/tok123"
for n in "$API" "$APPD"; do
  MSYS2_ARG_CONV_EXCL='/CN' openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 \
    -subj "/CN=$n" -addext "subjectAltName=DNS:$n" -keyout "$W/tls/$n.key" -out "$W/tls/$n.pem" >/dev/null 2>&1 || { echo "openssl failed"; exit 1; }
done

# Two releases of a static export, as `next build` with output: "export" writes it.
export_rel() {
  local r="$W/www/releases/$1"
  mkdir -p "$r/_next/static/chunks" "$r/admin/ledger"
  echo "<p>home of $1</p>" >"$r/index.html"
  echo "<p>not found ($1)</p>" >"$r/404.html"
  echo "<p>entry page of $1</p>" >"$r/admin/ledger/entry.html"
  echo "console.log('$1')" >"$r/_next/static/chunks/main-abc123.js"
}
export_rel one; export_rel two
ROOT="$W/www/releases/one"   # a plain directory; A14 switches a symlink where one can be made

cat >"$W/stub.py" <<'EOF'
import sys, json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
class H(BaseHTTPRequestHandler):
    def _do(self):
        b = json.dumps({"stub": True, "path": self.path}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    do_GET = do_POST = do_HEAD = _do
    def log_message(self, *a): pass
ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
EOF
up_open() { (exec 3<>"/dev/tcp/127.0.0.1/$UP") 2>/dev/null; }
start_upstream() {
  if up_open; then echo "port $UP already answers: a leftover stub? Refusing to test against it."; return 1; fi
  "$PY" "$W/stub.py" "$UP" & UP_PID=$!
  for _ in $(seq 50); do up_open && break; sleep 0.1; done
}
stop_upstream() {
  [[ -n $UP_PID ]] || return 0
  [[ -r /proc/$UP_PID/winpid ]] && taskkill //F //T //PID "$(cat "/proc/$UP_PID/winpid")" >/dev/null 2>&1
  kill "$UP_PID" 2>/dev/null
  for _ in $(seq 30); do up_open || break; sleep 0.1; done
  wait "$UP_PID" 2>/dev/null; UP_PID=""
}

render() {
  NGX_API_DOMAIN=$API NGX_APP_ORIGIN=$APP NGX_UPSTREAM="127.0.0.1:$UP" \
  NGX_HTTP_LISTEN="127.0.0.1:$HTTP" NGX_HTTPS_LISTEN="127.0.0.1:$HTTPS" \
  NGX_CERT="$(npath "$W/tls/$API.pem")" NGX_KEY="$(npath "$W/tls/$API.key")" NGX_ACME_ROOT="$(npath "$W/acme")" \
  NGX_APP_ROOT="$(npath "$ROOT")" NGX_APP_CERT="$(npath "$W/tls/$APPD.pem")" NGX_APP_KEY="$(npath "$W/tls/$APPD.key")" \
    bash "$D/nginx/render-public.sh" "$D/nginx/$1"
}
# configure TEMPLATE...: the server's sites for one phase.
configure() {
  local inc="" t
  for t in "$@"; do
    render "$t" >"$W/ngx/conf/$t.conf" 2>"$W/r.err" || { cat "$W/r.err"; return 1; }
    inc+="  include $(npath "$W/ngx/conf/$t.conf");"$'\n'
  done
  cat >"$W/ngx/conf/nginx.conf" <<EOF
worker_processes 1;
error_log $(npath "$W/ngx/logs/error.log") warn;
pid $(npath "$W/ngx/logs/nginx.pid");
events { worker_connections 64; }
http {
  access_log off;
  include $(npath "$W/mime.types");
  default_type application/octet-stream;
  client_body_temp_path $(npath "$W/ngx/temp/body");
  proxy_temp_path $(npath "$W/ngx/temp/proxy");
  fastcgi_temp_path $(npath "$W/ngx/temp/fastcgi");
  uwsgi_temp_path $(npath "$W/ngx/temp/uwsgi");
  scgi_temp_path $(npath "$W/ngx/temp/scgi");
$inc}
EOF
}
# The two types the export needs, as the packaged mime.types has them.
printf 'types {\n  text/html html;\n  application/javascript js;\n  text/plain txt;\n}\n' >"$W/mime.types"
ngx() { "$NGINX_BIN" -e "$(npath "$W/ngx/logs/error.log")" -p "$(npath "$W/ngx")/" -c "$(npath "$W/ngx/conf/nginx.conf")" "$@"; }
port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$HTTP") 2>/dev/null; }
start_nginx() {
  if port_open; then echo "port $HTTP already answers: a leftover nginx? Refusing to test it."; return 1; fi
  ngx -t >"$W/t.out" 2>&1 || { cat "$W/t.out"; return 1; }
  ngx -g 'daemon off;' >"$W/n.out" 2>&1 & NGX_PID=$!
  for _ in $(seq 50); do port_open && return 0; sleep 0.1; done
  cat "$W/n.out" "$W/ngx/logs/error.log" 2>/dev/null; return 1
}
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

A80()  { curl -s -o "$W/body" -D "$W/hdr" -w '%{http_code}' --resolve "$APPD:$HTTP:127.0.0.1" "$@"; }
A443() { curl -s -o "$W/body" -D "$W/hdr" -w '%{http_code}' --cacert "$W/tls/$APPD.pem" --resolve "$APPD:$HTTPS:127.0.0.1" "$@"; }
API443() { curl -s -o "$W/body" -D "$W/hdr" -w '%{http_code}' --cacert "$W/tls/$API.pem" --resolve "$API:$HTTPS:127.0.0.1" "$@"; }
hdr() { tr -d '\r' <"$W/hdr" | awk -v k="$1" 'BEGIN{IGNORECASE=1} tolower($0) ~ "^"tolower(k)":" {sub(/^[^:]*: */,""); print; exit}'; }
body() { cat "$W/body" 2>/dev/null; }
# page_headers WHAT: the headers every page carries; empty when all are there.
page_headers() {
  local miss=""
  [[ $(hdr X-Content-Type-Options) == nosniff ]] || miss+=" nosniff"
  [[ $(hdr X-Frame-Options) == DENY ]] || miss+=" X-Frame-Options"
  [[ $(hdr Content-Security-Policy) == "frame-ancestors 'none'" ]] || miss+=" frame-ancestors"
  [[ $(hdr Referrer-Policy) == strict-origin-when-cross-origin ]] || miss+=" Referrer-Policy"
  echo "$miss"
}

T="A15 an app template with its app values empty is a refusal, not a config with a hole"
bad=""
for v in NGX_APP_ROOT NGX_APP_CERT NGX_APP_KEY; do
  vals=(NGX_API_DOMAIN=$API NGX_APP_ORIGIN=$APP NGX_UPSTREAM=127.0.0.1:$UP NGX_HTTP_LISTEN=127.0.0.1:$HTTP
        NGX_HTTPS_LISTEN=127.0.0.1:$HTTPS NGX_CERT=c.pem NGX_KEY=k.pem NGX_ACME_ROOT=/acme
        NGX_APP_ROOT=/www NGX_APP_CERT=ac.pem NGX_APP_KEY=ak.pem "$v=")
  env "${vals[@]}" bash "$D/nginx/render-public.sh" "$D/nginx/argon-app-tls.conf.in" >/dev/null 2>&1 && bad+=" $v"
done
# and the control: all of them set renders
env NGX_API_DOMAIN=$API NGX_APP_ORIGIN=$APP NGX_UPSTREAM=127.0.0.1:$UP NGX_HTTP_LISTEN=127.0.0.1:$HTTP     NGX_HTTPS_LISTEN=127.0.0.1:$HTTPS NGX_CERT=c.pem NGX_KEY=k.pem NGX_ACME_ROOT=/acme     NGX_APP_ROOT=/www NGX_APP_CERT=ac.pem NGX_APP_KEY=ak.pem     bash "$D/nginx/render-public.sh" "$D/nginx/argon-app-tls.conf.in" >/dev/null 2>&1 || bad+=" (and it refused with every value set)"
[[ -z $bad ]] && pass || red "rendered with empty:$bad"

start_upstream || { echo "  RED  A00 the stub API would not start"; exit 1; }

# ---- Phase 1: the preview ------------------------------------------------------
if ! configure argon-acme.conf.in argon-api-tls.conf.in argon-app-preview.conf.in || ! start_nginx; then
  echo "  RED  A00 nginx would not start on the preview's sites (above)"; RED+=(A00)
else
T="A01 the preview serves a page on the app's name over port 80, with its headers"
c=$(A80 "http://$APPD:$HTTP/")
if [[ $c != 200 || $(body) != "<p>home of one</p>" ]]; then red "$c: $(body | head -c 120)"
elif m=$(page_headers); [[ -n $m ]]; then red "missing:$m"
else pass; fi

T="A02 a route without .html is its page, with its query"
c=$(A80 "http://$APPD:$HTTP/admin/ledger/entry?id=5f0c2a54-0000-4000-8000-000000000001")
if [[ $c != 200 || $(body) != "<p>entry page of one</p>" ]]; then red "$c: $(body | head -c 120)"; else pass; fi

T="A03 an unknown path is the 404 page, with status 404"
c=$(A80 "http://$APPD:$HTTP/no/such/page")
if [[ $c != 404 || $(body) != "<p>not found (one)</p>" ]]; then red "$c: $(body | head -c 120)"; else pass; fi

T="A04 nothing but GET and HEAD"
c=$(A80 -X POST --data 'pin=1234' "http://$APPD:$HTTP/")
if [[ $c != 403 ]]; then red "a POST answered $c"; else pass; fi

T="A05 the certificate check's file is served on the app's name"
c=$(A80 "http://$APPD:$HTTP/.well-known/acme-challenge/tok123")
if [[ $c != 200 || $(body) != token-for-the-check ]]; then red "$c: $(body | head -c 80)"; else pass; fi
stop_nginx
fi

# ---- Phase 2: live ---------------------------------------------------------------
if [[ -L $W/probe ]] || ln -s "releases/one" "$W/www/current" 2>/dev/null && [[ -L $W/www/current ]]; then
  ROOT="$W/www/current"; LINKS=1
else
  rm -rf "$W/www/current"; LINKS=0
fi
if ! configure argon-acme.conf.in argon-api-tls.conf.in argon-app-acme.conf.in argon-app-tls.conf.in || ! start_nginx; then
  echo "  RED  A00 nginx would not start on the live sites (above)"; RED+=(A00)
else
T="A06 port 80 redirects a GET to https on the app's own name, and still serves the check"
c=$(A80 "http://$APPD:$HTTP/admin/ledger/entry?id=x")
if [[ $c != 301 || $(hdr Location) != "https://$APPD/admin/ledger/entry?id=x" ]]; then red "$c to '$(hdr Location)'"
elif [[ $(A80 "http://$APPD:$HTTP/.well-known/acme-challenge/tok123") != 200 ]]; then red "the certificate check's file is not served once the certificate exists (renewals would fail)"
else pass; fi

T="A07 port 80 serves no page in the clear and takes no POST"
c=$(A80 -X POST --data 'pin=1234' "http://$APPD:$HTTP/")
if [[ $c != 403 ]]; then red "a POST on port 80 answered $c"
elif A80 "http://$APPD:$HTTP/" >/dev/null; body | grep -q "home of"; then red "port 80 served the page itself"
else pass; fi

T="A08 443 serves the app on its own certificate, with HSTS and the page headers"
c=$(A443 "https://$APPD:$HTTPS/")
if [[ $c != 200 || $(body) != "<p>home of one</p>" ]]; then red "$c: $(body | head -c 120)"
elif [[ $(hdr Strict-Transport-Security) != "max-age=31536000" ]]; then red "HSTS is '$(hdr Strict-Transport-Security)'"
elif m=$(page_headers); [[ -n $m ]]; then red "missing:$m"
else pass; fi

T="A09 hashed assets are cached for a year, immutable, with HSTS"
c=$(A443 "https://$APPD:$HTTPS/_next/static/chunks/main-abc123.js")
if [[ $c != 200 ]]; then red "$c"
elif [[ $(hdr Cache-Control) != "public, max-age=31536000, immutable" ]]; then red "Cache-Control is '$(hdr Cache-Control)'"
elif [[ $(hdr Strict-Transport-Security) != "max-age=31536000" ]]; then red "no HSTS on an asset"
else pass; fi

T="A10 pages are revalidated on every load (no-cache), so a release is seen at once"
A443 "https://$APPD:$HTTPS/admin/ledger/entry" >/dev/null
if [[ $(hdr Cache-Control) != no-cache ]]; then red "Cache-Control is '$(hdr Cache-Control)'"; else pass; fi

T="A11 an unknown path is the 404 page, status 404"
c=$(A443 "https://$APPD:$HTTPS/no/such/page")
if [[ $c != 404 || $(body) != "<p>not found (one)</p>" ]]; then red "$c: $(body | head -c 120)"; else pass; fi

T="A12 nothing but GET and HEAD on 443"
c=$(A443 -X POST --data 'x=1' "https://$APPD:$HTTPS/")
if [[ $c != 403 ]]; then red "a POST answered $c"; else pass; fi

T="A13 the API still answers on its own name and certificate beside the app (SNI)"
c=$(API443 "https://$API:$HTTPS/healthz")
if [[ $c != 200 ]] || ! body | grep -q '"stub": true'; then red "$c: $(body | head -c 120)"; else pass; fi

T="A14 a switched release is served without a reload"
if ((LINKS == 0)); then echo "  NOT RUN $T (this platform cannot make symlinks; CI runs it)"; RED+=(A14-NOT-RUN)
else
  ln -sfn "releases/two" "$W/www/current.new" && mv -Tf "$W/www/current.new" "$W/www/current"
  c=$(A443 "https://$APPD:$HTTPS/")
  if [[ $c != 200 || $(body) != "<p>home of two</p>" ]]; then red "after the switch: $c $(body | head -c 80)"; else pass; fi
fi
fi

echo
if ((${#RED[@]})); then echo "RED: ${RED[*]}"; exit 1; fi
echo "all 14 green"
