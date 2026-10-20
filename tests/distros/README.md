# Native managed lifecycle tests

```sh
tests/distros/run.sh debian /absolute/path/to/input arm64
tests/distros/run.sh arch /absolute/path/to/input amd64
tests/distros/run.sh void /absolute/path/to/input arm64

# Package production only (also used by the test runner):
tests/distros/build.sh debian 1.2.3 arm64 /absolute/path/to/input ./output
```

Input contains executable `ph`, actual `sing-box` 1.14.3 and `mihomo`
1.19.32, and `LICENSE-sing-box` / `LICENSE-mihomo` / `COPYING`. Prepare it with
`scripts/build-linux.sh VERSION ARCH INPUT_DIR`. Architectures are `amd64`
or `arm64` (host default for `run.sh`); Docker must support the selected
platform. Arch's public base image supports amd64 only. Prefer native-architecture
runners: local ARM/QEMU testing timed out bootstrapping Arch systemd 262, before
any ph service ran. GitHub CI uses a native amd64 runner for that lifecycle.

The builder calls `scripts/build-packages.sh DISTRO VERSION ARCH INPUT_DIR
OUTPUT_DIR` inside Debian trixie, Arch `archlinux:base`, or official
`ghcr.io/void-linux/void-glibc-full:latest`. The host needs Docker and Go, not native
packaging tools. `run.sh` rebuilds the CLI at 0.0.1 and 0.0.2 from the local
project, reusing the prepared cores and licenses; `build.sh` packages its input
unchanged. Builder images remain cached; test containers and temporary
packages are removed, with container/service logs printed on failure.

Tests require privileged containers: real systemd PID 1 (a minimal basic target,
private cgroup namespace with Docker-managed writable cgroups) or real Void
runsvdir PID 1. Systemd's binfmt service is masked so container startup cannot
reset the host's cross-architecture emulation. Supervisor readiness uses ten
timed probes; the lifecycle smoke test has a 300-second deadline. No simulated cores
or silent skips: local HTTP subscriptions feed a real Shadowsocks server,
and HTTP traffic traverses each installed core's mixed proxy.

The lifecycle covers native installation, managed defaults and private state,
subscription stdin/url_file, live selection and persistent pin, apply/update,
real scheduled update service restarts, both backend switches, rootless reads
and live use after switching, rootless mutation denial, failed-source rollback,
package version upgrade and retained state/local edits, active core restart,
and removal with preserved state and stopped services.
