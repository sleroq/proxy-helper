#!/bin/sh
# Prepare the self-contained input shared by native packages and distro tests.
set -eu
if [ "$#" -ne 3 ]; then
    echo "usage: $0 VERSION amd64|arm64 OUTPUT_DIR" >&2
    exit 2
fi
version=$1
arch=$2
output=$3
case "$version" in '' | *[!0-9A-Za-z.+-]*)
    echo "invalid version" >&2
    exit 2
    ;;
esac
case "$arch" in amd64 | arm64) ;; *)
    echo "unsupported architecture: $arch" >&2
    exit 2
    ;;
esac
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$output"
output=$(CDPATH='' cd -- "$output" && pwd)
cd "$root"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$output/ph" ./cmd/ph
"$root/scripts/fetch-cores.sh" "$arch" "$output"
