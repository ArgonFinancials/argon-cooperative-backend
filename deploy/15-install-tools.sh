#!/usr/bin/env bash
# HOST ONLY. Go toolchain (pinned, checksum-verified), golang-migrate CLI
# (module checksum verified by sum.golang.org), and the AWS CLI for backups.
source "$(dirname "$0")/lib.sh"
[[ $TARGET == host ]] || die "15-install-tools.sh is host-only"
[[ $EUID -eq 0 ]] || die "run with sudo"
require_env GO_VERSION GO_SHA256_ARM64 MIGRATE_VERSION

arch=$(dpkg --print-architecture)
[[ $arch == arm64 ]] || die "pinned checksum is for arm64; this host is $arch — set the matching checksum"

if [[ "$(/usr/local/go/bin/go version 2>/dev/null)" != "go version go$GO_VERSION linux/arm64" ]]; then
  info "Go $GO_VERSION"
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
  curl -fsSL -o "$tmp/go.tgz" "https://go.dev/dl/go$GO_VERSION.linux-arm64.tar.gz"
  echo "$GO_SHA256_ARM64  $tmp/go.tgz" | sha256sum -c --quiet - || die "Go tarball checksum mismatch"
  rm -rf /usr/local/go && tar -C /usr/local -xzf "$tmp/go.tgz"
fi
ok "$(/usr/local/go/bin/go version)"

info "migrate $MIGRATE_VERSION"
GOBIN=/usr/local/bin GOFLAGS=-mod=mod GOPATH=/root/go GOCACHE=/root/.cache/go-build \
  /usr/local/go/bin/go install -tags postgres \
  "github.com/golang-migrate/migrate/v4/cmd/migrate@$MIGRATE_VERSION"
# `migrate -version` says "dev" for any `go install` build, so it proves
# nothing (found on the first EC2 run). The module version is in the binary.
got=$(/usr/local/go/bin/go version -m /usr/local/bin/migrate | awk '$1 == "mod" && $2 == "github.com/golang-migrate/migrate/v4" {print $3}')
[[ $got == "$MIGRATE_VERSION" ]] || die "migrate is module version '${got:-unknown}', not $MIGRATE_VERSION"
ok "migrate $got (module version, read from the binary)"

if [[ $BACKUP_STORE == s3://* ]] && ! command -v aws >/dev/null; then
  info "AWS CLI (snap, published by Amazon)"
  snap install aws-cli --classic
fi
# As the last command, a bare `command -v aws && ok` made the whole script
# exit 1 whenever the AWS CLI was rightly skipped (found on the first EC2 run).
if command -v aws >/dev/null; then
  ok "$(aws --version)"
else
  info "AWS CLI not installed: BACKUP_STORE is not s3:// (see the stated gap in deploy/README.md)"
fi
