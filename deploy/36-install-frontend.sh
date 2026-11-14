#!/usr/bin/env bash
# THE SERVER. Installs the release's frontend (a static export, built in CI
# from the commit deploy/frontend.ref pins) where nginx serves it, and points
# the site at it in one rename. Run by argon-deploy after the probe has passed
# and the API is up, so a failed deploy never shows a new frontend against an
# old API.
#
#   <release>/frontend/           the export, as CI built it (index.html, 404.html, _next/)
#   <release>/FRONTEND_COMMIT     the frontend commit it was built from
#
#   $APP_WWW/releases/<release>/  one directory per release, never edited
#   $APP_WWW/releases/<release>.commit
#   $APP_WWW/current              a symlink to one of them: nginx's root
#
# A release with no frontend changes nothing and says so (before the pin
# exists, and for a backend-only release the pin did not move: the same
# export is installed again under the new release name, which is cheap and
# keeps "one release, one state" literal).
#
# Refused, with current left as it was: no index.html or 404.html; any
# symlink, device or other non-regular file in it (nothing in the export may
# point outside it); more than 200 MB.
# Keeps the current release and the two before it; older ones are removed.
#
#   sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/36-install-frontend.sh
#   ARGON_APP_WWW=<dir>   another place (the tests)
# Tests: test/ops/frontend_install_test.sh F01-F08.
source "$(dirname "$0")/lib.sh"

APP_WWW=${ARGON_APP_WWW:-/var/www/argon-frontend}
src="$REPO_DIR/frontend"
rel=${ARGON_RELEASE:-$(cat "$REPO_DIR/RELEASE" 2>/dev/null || date -u +%Y%m%dt%H%M%Sz)}
[[ $rel =~ ^[0-9a-z-]+$ ]] || die "release name '$rel' is not one"

if [[ ! -d $src ]]; then
  info "this release carries no frontend: the site is unchanged ($(readlink "$APP_WWW/current" 2>/dev/null || echo 'none installed'))"
  exit 0
fi

# What nginx would serve must be the export and nothing else.
[[ -f $src/index.html && -f $src/404.html ]] || die "the frontend has no index.html or 404.html: not a static export; the site is unchanged"
odd=$(find "$src" ! -type f ! -type d | head -5)
[[ -z $odd ]] || die "the frontend contains something that is not a file or a directory (nothing may point outside it): $(echo $odd); the site is unchanged"
kb=$(du -sk "$src" | cut -f1)
((kb <= 200000)) || die "the frontend is ${kb} KB, over the 200 MB limit; the site is unchanged"

install -d -m 0755 "$APP_WWW" "$APP_WWW/releases"
dest="$APP_WWW/releases/$rel"
[[ ! -e $dest ]] || die "release $rel is already installed at $dest: a release is never installed twice"
tmp="$APP_WWW/releases/.incoming-$rel"
rm -rf "$tmp"
cp -r "$src" "$tmp"
if ((EUID == 0)); then chown -R root:root "$tmp"; fi
find "$tmp" -type d -exec chmod 0755 {} + && find "$tmp" -type f -exec chmod 0644 {} +
mv "$tmp" "$dest"
commit=$(cat "$REPO_DIR/FRONTEND_COMMIT" 2>/dev/null || true)
[[ -z $commit || $commit =~ ^[0-9a-f]{40}$ ]] || die "FRONTEND_COMMIT is not a commit id"
echo "${commit:-unknown}" > "$dest.commit"

# One rename: nginx sees the old release or the new one, never neither.
ln -sfn "releases/$rel" "$APP_WWW/current.new"
mv -Tf "$APP_WWW/current.new" "$APP_WWW/current"
[[ $(readlink "$APP_WWW/current") == "releases/$rel" && -f $APP_WWW/current/index.html ]] ||
  die "the site does not point at release $rel after the switch"
ok "the frontend serves release $rel (frontend commit ${commit:0:12})"

# Keep the current release and the two before it.
mapfile -t old < <(cd "$APP_WWW/releases" && ls -1d */ 2>/dev/null | sed 's#/$##' | sort -r | tail -n +4)
for o in "${old[@]}"; do
  [[ $o == "$rel" ]] && continue
  rm -rf "${APP_WWW:?}/releases/$o" "$APP_WWW/releases/$o.commit"
done
