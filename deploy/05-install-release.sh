#!/usr/bin/env bash
# THE SERVER. Installs this checkout as THE RELEASE at /opt/argon/src: what the
# server's scripts, timers and image build all run from. A later checkout, or a
# CI upload, never changes the code under a running timer: it lands beside the
# release and replaces it in one rename. The previous release is kept as
# /opt/argon/src.old for one step back.
#
# Run from a checkout:  sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/05-install-release.sh
source "$(dirname "$0")/lib.sh"
on_server || die "05-install-release.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"
[[ $REPO_DIR != /opt/argon/src ]] || die "run from a checkout, not from the installed release itself"

# A Windows checkout (or git archive under core.autocrlf) delivers CRLF, and a
# CRLF script fails only when it first runs (`$'bash\r'`). Refuse it here.
crlf=$(cd "$REPO_DIR" && grep -rlI $'\r' --exclude='*.md' deploy migrations Dockerfile .dockerignore 2>/dev/null || true)
[[ -z $crlf ]] || die "CRLF line endings in the release (check .gitattributes): $(echo $crlf)"

install -d -m 0755 /opt/argon /opt/argon/bin
rel=${ARGON_RELEASE:-$(date -u +%Y%m%dt%H%M%Sz)}
new=/opt/argon/src.new
rm -rf "$new"
cp -r "$REPO_DIR" "$new"
rm -rf "$new/.git" "$new/bin" "$new/.backups"
rm -f "$new"/deploy/env/*.env            # examples only: secrets live in /etc/argon
echo "$rel" > "$new/RELEASE"
chown -R root:root "$new"; chmod -R go-w "$new"
rm -rf /opt/argon/src.old
[[ -d /opt/argon/src ]] && mv /opt/argon/src /opt/argon/src.old
mv "$new" /opt/argon/src
ok "release $rel installed at /opt/argon/src$( [[ -d /opt/argon/src.old ]] && echo " (previous kept at /opt/argon/src.old)")"
info "from here, run every step from the release:  cd /opt/argon/src"
