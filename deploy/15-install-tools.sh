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
ok "$(/usr/local/bin/migrate -version 2>&1)"

if [[ $BACKUP_STORE == s3://* ]] && ! command -v aws >/dev/null; then
  info "AWS CLI (snap, published by Amazon)"
  snap install aws-cli --classic
fi
command -v aws >/dev/null && ok "$(aws --version)"
