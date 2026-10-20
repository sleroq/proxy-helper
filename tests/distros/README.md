# Native package builds and lifecycle tests

Run from the repository root with Go, curl, sha256sum, and Docker installed.
Docker must support the selected platform and privileged containers.

```sh
# Build the CLI, verified upstream cores, and license files.
scripts/build-linux.sh 1.2.3 amd64 build/input

# Produce a package without running lifecycle tests.
tests/distros/build.sh debian 1.2.3 amd64 build/input dist

# Test installation, operation, upgrade, and removal.
tests/distros/run.sh debian build/input amd64
```

Replace `debian` with `arch` or `void`. Architectures are `amd64` and `arm64`;
Arch supports only `amd64`. `run.sh` defaults to the host architecture. Prefer
native-architecture runners.

`build.sh` packages its input unchanged using native tools in distro containers.
`run.sh` rebuilds the local CLI as versions 0.0.1 and 0.0.2, reusing the input's
cores and licenses. Builder images are cached; test containers and temporary
packages are removed, with logs printed on failure.

The suite runs real systemd or runit, local HTTP subscription fixtures, and a
real Shadowsocks server. It checks both cores' proxy traffic, selection/pins,
apply/update, scheduled updates, failed-update rollback, unprivileged access
boundaries, upgrades preserving state/configuration, and removal. It uses no
provider credentials or host TUN routing.

CI runs Debian and Void on amd64/arm64 and Arch on amd64, plus uncached
`go test -race -count=1 ./...` on Linux/macOS and Go analysis on Linux. Successful
`vVERSION` releases publish packages, checksums, and corresponding bundled-core
source archives. Core pins are in `scripts/fetch-cores.sh`; source archives are
built by `scripts/core-sources.sh`.

For installation, configuration, and security requirements, see
[Managed Linux installations](../../docs/linux.md).
