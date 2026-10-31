#!/usr/bin/env bash
# check-origin.sh ORIGIN API_DOMAIN
#
# Once the API has a public name, ARGON_ALLOWED_ORIGINS is the app's ONE https
# origin, and nothing else (going live, 2026-10-30). And it must be under the
# API's own domain: the session cookie reaches the API only when the app and
# the API share one registrable domain (LEDGER_CONTRACT.md §12). An app on
# another domain is every call cross-site, and no SameSite value sends it.
#
# "The API's own domain" is the API name minus its first label
# (api.example.com -> example.com). Exact for a name one
# level below the registrable domain, which is how this deployment is named;
# it never needs a public-suffix list.
#
# Used by 30-install-api.sh (before the value reaches the API) and by
# nginx/render-public.sh (before it reaches nginx). Exit 0 = acceptable.
set -euo pipefail
origin=${1-}; api=${2:?usage: check-origin.sh ORIGIN API_DOMAIN}
fail() { echo "ARGON_ALLOWED_ORIGINS: $*" >&2; exit 1; }

[[ -n $origin ]] || fail "empty, and the API has a public name ($api): set it to the app's https origin"
[[ $origin != *,* ]] || fail "more than one origin: once live it is the app's origin and nothing else"
[[ $origin =~ ^https://([a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+)$ ]] ||
  fail "'$origin' is not https://host (no path, port, wildcard or trailing slash)"
host=${BASH_REMATCH[1]}
[[ $api == *.* ]] || fail "API name '$api' has no domain"
parent=${api#*.}
[[ $host == "$parent" || $host == *".$parent" ]] ||
  fail "'$origin' is not under $parent, the API's domain: the session cookie would never be sent"
