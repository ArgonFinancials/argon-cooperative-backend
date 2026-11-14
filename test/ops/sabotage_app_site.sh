#!/usr/bin/env bash
# The red half for test/ops/app_site_test.sh and test/ops/frontend_install_test.sh:
# copies deploy/, breaks one rule per copy, runs the tests against the copy,
# and requires the named tests to go red. A break whose text is no longer in
# the file stops the run (a sabotage that does not apply proves nothing).
#
#   bash test/ops/sabotage_app_site.sh            (the site: needs nginx)
#   bash test/ops/sabotage_app_site.sh install    (the installer: needs symlinks, i.e. Linux)
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
M=$(mktemp -d)
trap 'rm -rf "$M"' EXIT
python3 -c '' 2>/dev/null && py=python3 || py=python
which=${1:-site}

bad=0
# sabotage NAME FILE EXPECT OLD NEW
sabotage() {
  local name=$1 file=$2 exp=$3 old=$4 new=$5 d="$M/$1"
  mkdir -p "$d"; cp -r "$repo/deploy" "$d/deploy"
  "$py" - "$old" "$new" "$d/deploy/$file" <<'PY' || { echo "$name: its text is not in $file (once)"; bad=1; return; }
import sys
old, new, p = sys.argv[1:]
t = open(p, encoding="utf-8").read()
if t.count(old) != 1: sys.exit(1)
open(p, "w", encoding="utf-8", newline="\n").write(t.replace(old, new))
PY
  local rc
  if [[ $which == site ]]; then GL_DEPLOY="$d/deploy" bash "$here/app_site_test.sh" >"$d/log" 2>&1; rc=$?
  else DEPLOY_SRC="$d/deploy" bash "$here/frontend_install_test.sh" >"$d/log" 2>&1; rc=$?; fi
  local reds miss="" e v
  reds=$(grep -oE '^  RED  [AF][0-9]+' "$d/log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  for e in $exp; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then v="WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then v="STAYED GREEN on:$miss"; bad=1
  else v="red as expected"; fi
  echo "$name | expect $exp | red: $reds| $v"
}

if [[ $which == site ]]; then
  P=nginx/argon-app-preview.conf.in; L=nginx/argon-app-tls.conf.in; C=nginx/argon-app-acme.conf.in
  # The page headers dropped from the preview's pages.
  sabotage preview_no_nosniff "$P" "A01" \
    "        add_header Cache-Control \"no-cache\" always;
        add_header X-Content-Type-Options nosniff always;" "        add_header Cache-Control \"no-cache\" always;"
  # Routes without .html not found (the export's pages are entry.html).
  sabotage preview_no_html "$P" "A02" 'try_files $uri $uri.html $uri/index.html =404;' 'try_files $uri $uri/index.html =404;'
  # nginx's own 404 page instead of the export's.
  sabotage preview_no_404_page "$P" "A03" '    error_page 404 /404.html;' ''
  # A POST served.
  sabotage preview_any_method "$P" "A04" "    location / {
        limit_except GET { deny all; }" "    location / {"
  # No certificate check on the preview: the first certificate can never be issued.
  sabotage preview_no_acme "$P" "A05" "    location ^~ /.well-known/acme-challenge/ {
        root @ACME_ROOT@;
        default_type text/plain;
        try_files \$uri =404;
    }
" ""
  # Port 80 keeps serving pages in the clear after the certificate.
  sabotage acme_serves_pages "$C" "A06 A07" "        return 301 https://@APP_DOMAIN@\$request_uri;" "        root @APP_ROOT@; try_files \$uri \$uri.html \$uri/index.html =404;"
  # The certificate check dropped once the certificate exists: renewals fail in 90 days.
  sabotage acme_no_renewal "$C" "A06" "    location ^~ /.well-known/acme-challenge/ {
        root @ACME_ROOT@;
        default_type text/plain;
        try_files \$uri =404;
    }
" ""
  # HSTS only where the server block says it (dropped from a location).
  sabotage tls_page_no_hsts "$L" "A08" "        add_header Strict-Transport-Security \"max-age=31536000\" always;
        add_header Cache-Control \"no-cache\" always;" "        add_header Cache-Control \"no-cache\" always;"
  sabotage tls_asset_no_hsts "$L" "A09" "        add_header Strict-Transport-Security \"max-age=31536000\" always;
        add_header Cache-Control \"public, max-age=31536000, immutable\" always;" "        add_header Cache-Control \"public, max-age=31536000, immutable\" always;"
  # Assets not cached.
  sabotage tls_asset_uncached "$L" "A09" 'add_header Cache-Control "public, max-age=31536000, immutable" always;' 'add_header Cache-Control "no-cache" always;'
  # Pages cached: a release unseen until the cache expires.
  sabotage tls_pages_cached "$L" "A10" 'add_header Cache-Control "no-cache" always;' 'add_header Cache-Control "public, max-age=86400" always;'
  sabotage tls_no_404_page "$L" "A11" '    error_page 404 /404.html;' ''
  sabotage tls_any_method "$L" "A12" "    location / {
        limit_except GET { deny all; }" "    location / {"
  # The app claims the default server: nginx refuses a second one on 443 and
  # does not start (A00), rather than taking the API's unknown names.
  sabotage tls_default_server "$L" "A00" "    listen @HTTPS_LISTEN@ ssl;" "    listen @HTTPS_LISTEN@ ssl default_server;"
  # An app template rendered with an empty app value (a config with a hole).
  sabotage render_app_empty_ok nginx/render-public.sh "A15" 'if [[ -z ${!v:-} ]]; then echo "render-public: $v is empty (the template uses it)"' 'if false; then echo "render-public: $v is empty (the template uses it)"'
  # The app served on the API's certificate.
  sabotage tls_api_cert "$L" "A08" "    ssl_certificate @APP_CERT@;
    ssl_certificate_key @APP_KEY@;" "    ssl_certificate @CERT@;
    ssl_certificate_key @KEY@;"
else
  I=36-install-frontend.sh
  sabotage no_index_check "$I" "F04" '[[ -f $src/index.html && -f $src/404.html ]] ||' 'true ||'
  sabotage no_link_check "$I" "F05" '[[ -z $odd ]] ||' 'true ||'
  sabotage no_prune "$I" "F03" '| sort -r | tail -n +4)' '| sort -r | tail -n +99)'
  sabotage reinstall "$I" "F08" '[[ ! -e $dest ]] ||' 'rm -rf "$dest"; true ||'
  sabotage commit_inside "$I" "F07" 'echo "${commit:-unknown}" > "$dest.commit"' 'echo "${commit:-unknown}" > "$dest/FRONTEND_COMMIT"'
  sabotage switch_skipped "$I" "F01 F02" 'mv -Tf "$APP_WWW/current.new" "$APP_WWW/current"' 'true'
  sabotage silent_no_frontend "$I" "F06" '  info "this release carries no frontend' '  : "this release carries no frontend'
fi

((bad)) && { echo "SABOTAGE NOT CAUGHT"; exit 1; }
echo "every sabotaged copy was caught"
