# Managed Linux installations

Native packages are the recommended Linux installation. They install the CLI,
verified upstream sing-box and mihomo executables, editable configuration, and
system services. No Nix or Go installation is required on the target host.
The XDG `ph init` profile is reserved for manual testing or custom deployments.

| Distribution | Package | Architectures | Supervisor |
| --- | --- | --- | --- |
| Debian 13+ | `.deb` | amd64, arm64 | systemd |
| Arch Linux | `.pkg.tar.zst` | x86_64 | systemd |
| Void Linux (glibc) | `.xbps` | x86_64, aarch64 | runit |

Void musl, Arch Linux ARM, other init systems, and TUN/firewall integration are
not covered by this packaging/test matrix. The default is a loopback mixed
HTTP/SOCKS proxy at `127.0.0.1:1080` and a loopback Clash API at port 9090.
Applications must use the proxy explicitly; installation does not reroute the host.

## Install or upgrade

Download the appropriate package and `SHA256SUMS` from the repository's
[GitHub Releases](https://github.com/sleroq/proxy-helper/releases). Verify the
package's checksum before installing. Release assets are published when a
`vVERSION` tag passes the test workflow; there is no hosted apt/pacman repository.

Install with your native package manager (substitute the downloaded filename):

```sh
# Debian
sudo apt install ./ph_VERSION_amd64.deb

# Arch
sudo pacman -U ./ph-VERSION-1-x86_64.pkg.tar.zst

# Void: put the .xbps file in a directory and generate its repository index
mkdir -p ph-packages
mv ph-VERSION_1.x86_64.xbps ph-packages/
xbps-rindex -a ph-packages/*.xbps
sudo xbps-install --repository "$PWD/ph-packages" ph
```

To upgrade, download and verify the newer package and repeat the native install
command. On Void, include `-u` to update an installed package. Package upgrades
preserve locally edited `/etc/ph` files and private state and restart an active
proxy with the new executables. Resolve `.pacnew`/`.new-*` files using your
package manager's normal configuration migration procedure. This updates the
CLI and bundled cores; **`ph update` updates subscriptions, not software**.

Fresh installs enable the services and subscription scheduler but have no
usable proxy configuration until you add sources and seed the state. Hooks do
not download subscriptions or import an existing user's XDG configuration.

## First configuration

Put your subscription URL in a root-owned private file, preferably using your
secret manager or `sudoedit`, rather than putting credentials in shell history.

```sh
sudo install -m 0600 /dev/null /etc/ph/provider-url
sudoedit /etc/ph/provider-url
sudo ph subscription add <<'JSON'
{"id":"main","url_file":"/etc/ph/provider-url","prefix":"main-"}
JSON
sudo ph update
ph status
ph list
```

A successful update validates and installs `/var/lib/ph/config.json`, then starts
or restarts the managed proxy. For package-level settings, edit
`/etc/ph/config.json`; native templates are `/etc/ph/sing-box.json` and
`/etc/ph/mihomo.json`. The two cores share state and never run concurrently.

```sh
ph use 'main-node'                   # live-only; any local user
sudo ph pin 'main-node'              # persistent; state owner
sudo ph backend select mihomo        # validate, switch, readiness check
sudo ph backend select sing-box
sudo ph apply                       # activate cached nodes and staged policy
sudo ph refresh                     # fixed native subscription update service
```

`--config PATH` explicitly selects another profile. `ph init` still creates a
user-owned XDG profile with port 2080 for manual testing. Never point a root-run
managed service at a user-writable profile, registry, template or cache.

## Services, scheduling and logs

**Debian and Arch:** `ph-proxy.service` runs `ph run` using the already validated
config. `ph-proxy-update.service` is a root oneshot; its timer runs five minutes
after boot and daily, catching up missed calendar events. Inspect or trigger it:

```sh
systemctl status ph-proxy.service ph-proxy-update.timer
sudo systemctl start ph-proxy-update.service
sudo journalctl -u ph-proxy -u ph-proxy-update
```

**Void:** `/etc/sv/ph-proxy` and `/etc/sv/ph-proxy-update` are enabled in
`/var/service` on a running runit host. The update service attempts an update at
startup, sleeps for 24 hours, and is supervised again for the next update.
`sudo ph refresh` restarts that service to request an earlier update; its return
is not confirmation that fetching or activation succeeded. Logs are private
under `/var/log/ph-proxy` and `/var/log/ph-proxy-update`.

```sh
sudo sv status ph-proxy ph-proxy-update
sudo sv restart ph-proxy-update
sudo tail -f /var/log/ph-proxy-update/current
```

If installing into an offline root/image rather than a booted system, enable
services after boot: `systemctl enable --now ph-proxy-update.timer` and
`systemctl enable ph-proxy.service`, or create the two `/var/service` symlinks
for runit. Seed with `sudo ph update` before expecting proxy traffic.

## Ownership and removal

The daemon and updater run as root, matching the managed Nix profile. Root owns
`/etc/ph` and `/var/lib/ph`. Subscription URLs, registry, caches, generated config,
mode and pin state are private; manifest/health/backend identity are public
metadata. Persistent CLI edits require the state owner's permissions, but the
unauthenticated loopback API is **not** a selection-only permission gate: core
endpoints also expose runtime configuration controls (mihomo accepts replacement
configuration through `/configs`). All local users/processes are therefore
trusted to control the running core. Do not use these defaults on hosts with
untrusted local users. No broad sudo or polkit permission is installed; the
`ph refresh` service trigger and persistent CLI changes require authorization.
Initialization refuses pre-existing user-owned/writable state instead of
silently promoting it into privileged input.

Normal package removal stops/disables the services but keeps private state and
edited configuration. Debian `apt purge ph` also removes native conffiles;
`/var/lib/ph` and provider secret files are deliberately not deleted. Review
backups and remove secrets manually when retiring a host. No bypass/tunnel mode
is a firewall kill switch.

## Build and automatic verification

Build input and a native package with Go, curl, sha256sum and Docker installed:

```sh
scripts/build-linux.sh 0.1.0 amd64 build/input
tests/distros/build.sh debian 0.1.0 amd64 build/input dist
# Change debian to arch or void as appropriate.
tests/distros/run.sh debian build/input amd64
```

The lifecycle suite uses actual systemd/runit in isolated privileged Docker
containers, a local HTTP subscription fixture and a real Shadowsocks server.
It checks native installation, initialization, running proxy traffic with both
cores, selection/pins, scheduled subscription updates, failed updates, upgrades
preserving state/config, unprivileged boundaries and removal. It does not use
real provider credentials or host TUN routing. See [the test harness](../tests/distros/README.md).

GitHub CI runs uncached `go test -race -count=1 ./...` on Linux/macOS, Go analysis,
and the five
supported distro/architecture pairs. Tagged releases publish native packages,
checksums, and vendored corresponding source archives for the bundled GPL cores.
Pinned upstream versions/checksums are in `scripts/fetch-cores.sh`; bump them
with `scripts/core-sources.sh` versions and package attribution when updating.
