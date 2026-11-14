#!/usr/bin/env bash
# render-public.sh TEMPLATE  ->  the rendered nginx config on stdout.
#
# The one way deploy/nginx/*.conf.in become config: 86-install-tls.sh renders
# with the server's values, test/ops/going_live_test.sh with test values, so the
# test runs what the server runs. Every NGX_* value is required, and a
# placeholder left in the output is a refusal, never a config with a hole.
#
#   NGX_API_DOMAIN    the API's public name             api.example.com
#   NGX_APP_ORIGIN    the app's origin (check-origin)   https://app.example.com
#   NGX_UPSTREAM      the API, host:port                127.0.0.1:8080
#   NGX_HTTP_LISTEN   addr:port for plain HTTP          0.0.0.0:80
#   NGX_HTTPS_LISTEN  addr:port for HTTPS               0.0.0.0:443
#   NGX_CERT, NGX_KEY the certificate and its key
#   NGX_ACME_ROOT     the directory the certificate check is served from
# The app's site (argon-app-*.conf.in, 2026-11-14) adds, each required only by
# a template that uses it:
#   NGX_APP_ROOT      the static export's directory     /var/www/argon-frontend/current
#   NGX_APP_CERT, NGX_APP_KEY  the app's own certificate and its key
# and @APP_DOMAIN@, which is NGX_APP_ORIGIN's host (one source, so the name
# served and the origin the API allows cannot differ).
set -euo pipefail
tpl=${1:?usage: render-public.sh TEMPLATE}
here="$(cd "$(dirname "$0")" && pwd)"
for v in NGX_API_DOMAIN NGX_APP_ORIGIN NGX_UPSTREAM NGX_HTTP_LISTEN NGX_HTTPS_LISTEN NGX_CERT NGX_KEY NGX_ACME_ROOT; do
  [[ -n ${!v:-} ]] || { echo "render-public: $v is empty" >&2; exit 1; }
  # sed's delimiter is | and & means the match: refuse both rather than escape them.
  [[ ${!v} != *[\|\&\;\'\"\ ]* ]] || { echo "render-public: $v contains a character nginx config cannot take here" >&2; exit 1; }
done
[[ $NGX_API_DOMAIN =~ ^[a-z0-9.-]+$ ]] || { echo "render-public: NGX_API_DOMAIN '$NGX_API_DOMAIN' is not a host name" >&2; exit 1; }
bash "$here/check-origin.sh" "$NGX_APP_ORIGIN" "$NGX_API_DOMAIN"
app_domain=${NGX_APP_ORIGIN#https://}
for v in NGX_APP_ROOT NGX_APP_CERT NGX_APP_KEY; do
  grep -q "@${v#NGX_}@" "$tpl" || continue
  if [[ -z ${!v:-} ]]; then echo "render-public: $v is empty (the template uses it)" >&2; exit 1; fi
  [[ ${!v} != *[\|\&\;\'\"\ ]* ]] || { echo "render-public: $v contains a character nginx config cannot take here" >&2; exit 1; }
done

out=$(sed -e "s|@API_DOMAIN@|$NGX_API_DOMAIN|g" -e "s|@APP_ORIGIN@|$NGX_APP_ORIGIN|g" \
          -e "s|@UPSTREAM@|$NGX_UPSTREAM|g" -e "s|@HTTP_LISTEN@|$NGX_HTTP_LISTEN|g" \
          -e "s|@HTTPS_LISTEN@|$NGX_HTTPS_LISTEN|g" -e "s|@CERT@|$NGX_CERT|g" \
          -e "s|@KEY@|$NGX_KEY|g" -e "s|@ACME_ROOT@|$NGX_ACME_ROOT|g" \
          -e "s|@APP_DOMAIN@|$app_domain|g" -e "s|@APP_ROOT@|${NGX_APP_ROOT:-}|g" \
          -e "s|@APP_CERT@|${NGX_APP_CERT:-}|g" -e "s|@APP_KEY@|${NGX_APP_KEY:-}|g" "$tpl")
if grep -qE '@[A-Z_]+@' <<<"$out"; then
  echo "render-public: placeholders left in $tpl: $(grep -oE '@[A-Z_]+@' <<<"$out" | sort -u | tr '\n' ' ')" >&2; exit 1
fi
printf '%s\n' "$out"
