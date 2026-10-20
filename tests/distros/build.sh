#!/bin/sh
set -eu
[ "$#" -eq 5 ] || { echo 'usage: tests/distros/build.sh debian|arch|void VERSION amd64|arm64 INPUT_DIR OUTPUT_DIR' >&2; exit 2; }
distro=$1 version=$2 arch=$3
case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
case "$arch" in amd64|arm64) ;; *) echo "unsupported architecture: $arch" >&2; exit 2 ;; esac
case "$distro" in
 debian) base=debian:trixie ;;
 arch) [ "$arch" = amd64 ] || { echo 'official archlinux:base supports amd64 only' >&2; exit 2; }; base=archlinux:base ;;
 void) base=ghcr.io/void-linux/void-glibc-full:latest ;;
 *) echo "unsupported distro: $distro" >&2; exit 2 ;;
esac
root=$(CDPATH='' cd -- "$(dirname "$0")/../.." && pwd)
input=$(cd "$4" && pwd)
mkdir -p "$5"
output=$(cd "$5" && pwd)
image=sb-distro-builder:$distro-$arch
docker build --platform "linux/$arch" --build-arg "BASE=$base" --build-arg "DISTRO=$distro" -t "$image" "$root/tests/distros"
docker run --rm --platform "linux/$arch" -v "$root:/project:ro" -v "$input:/input:ro" -v "$output:/artifacts" "$image" bash /project/scripts/build-packages.sh "$distro" "$version" "$arch" /input /artifacts
if [ "$distro" = void ]; then
 docker run --rm --platform "linux/$arch" -v "$output:/artifacts" "$image" sh -c 'xbps-rindex -a /artifacts/*.xbps'
fi
printf 'Packages: %s\n' "$output"
