#!/bin/sh
# Ship buildable corresponding sources alongside the bundled GPL executables.
set -eu
[ "$#" -eq 1 ] || { echo "usage: $0 OUTPUT_DIR" >&2; exit 2; }
mkdir -p "$1"
output=$(CDPATH='' cd -- "$1" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
for core in sing-box mihomo; do
    case "$core" in
        sing-box) repo=SagerNet/sing-box; version=1.14.3 ;;
        mihomo) repo=MetaCubeX/mihomo; version=1.19.32 ;;
    esac
    name=$core-$version
    curl -fL --retry 3 -o "$tmp/source.tar.gz" "https://codeload.github.com/$repo/tar.gz/refs/tags/v$version"
    tar -xzf "$tmp/source.tar.gz" -C "$tmp"
    # Include dependency sources, rather than relying on a future module proxy.
    (cd "$tmp/$name"; go mod vendor)
    printf 'Upstream: https://github.com/%s/tree/v%s\nBuild with the Go version in go.mod and -mod=vendor.\nSee upstream build/release scripts for build tags and flags.\n' "$repo" "$version" > "$tmp/$name/SB-SOURCE.txt"
    tar -czf "$output/$name-source.tar.gz" -C "$tmp" "$name"
done
