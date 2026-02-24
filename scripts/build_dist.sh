#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-"${ROOT_DIR}/dist"}"
CGO_ENABLED="${CGO_ENABLED:-0}"

cd "${ROOT_DIR}"
mkdir -p "${DIST_DIR}"

echo "==> generate init bundle"
go generate ./internal/bootstrap

build_one() {
  local goos="$1"
  local goarch="$2"
  local out="$3"

  echo "==> build ${goos}/${goarch} -> ${out}"
  env CGO_ENABLED="${CGO_ENABLED}" GOOS="${goos}" GOARCH="${goarch}" \
    go build -o "${DIST_DIR}/${out}" ./cmd/agent
}

build_one linux amd64 "xinghebot-linux-amd64"
build_one linux arm64 "xinghebot-linux-arm64"
build_one windows amd64 "xinghebot-windows-amd64.exe"
build_one windows arm64 "xinghebot-windows-arm64.exe"

echo "==> done"
ls -1 "${DIST_DIR}" | sed 's/^/ - /'
