# Managed Linux installations

Native packages include `ph`, verified upstream sing-box and mihomo binaries,
configuration, and services. The target host needs neither Go nor Nix.

| Distribution | Package | Architectures | Supervisor |
| --- | --- | --- | --- |
| Debian 13+ | `.deb` | amd64, arm64 | systemd |
| Arch Linux | `.pkg.tar.zst` | x86_64 | systemd |
| Void Linux (glibc) | `.xbps` | x86_64, aarch64 | runit |

The default mixed HTTP/SOCKS proxy is `127.0.0.1:1080`; the core API is
`127.0.0.1:9090`. Applications must opt into the proxy. Packages do not configure
TUN, host routing, or a firewall kill switch.

## Install and configure

Download a package and `SHA256SUMS` from
[GitHub Releases](https://github.com/sleroq/proxy-helper/releases), verify its
checksum, then install it (substitute the downloaded filename):

```sh
# Debian
sudo apt install ./ph_VERSION_amd64.deb
# Arch
sudo pacman -U ./ph-VERSION-1-x86_64.pkg.tar.zst
# Void
mkdir -p ph-packages
mv ph-VERSION_1.x86_64.xbps ph-packages/
xbps-rindex -a ph-packages/*.xbps
sudo xbps-install --repository "$PWD/ph-packages" ph
```

Services and subscription scheduling are enabled on a running supported host,
but require a successful subscription update before proxying. Store subscription
credentials in a private file rather than shell history:

```sh
sudo install -m 0600 /dev/null /etc/ph/provider-url
sudoedit /etc/ph/provider-url
sudo ph subscription add --id main --prefix main- --url-file /etc/ph/provider-url
sudo ph update
ph status
ph list
```

Package settings and core templates live in `/etc/ph`; state is in `/var/lib/ph`.
The cores share state and run one at a time.

```sh
ph use 'main-node'                  # live selection
sudo ph pin 'main-node'             # persistent selection
sudo ph backend select mihomo      # or sing-box
sudo ph apply                      # activate cached nodes and staged policy
sudo ph refresh                    # request the native subscription update service
```

`ph update` updates subscriptions, not software. To upgrade software, repeat the
native package installation (add `-u` on Void). Upgrades retain state and edited
configuration and restart an active proxy; reconcile package-manager configuration
files such as `.pacnew` or `.new-*` as needed.

## Services and logs

On Debian and Arch, the update timer runs five minutes after boot and daily,
catching up missed calendar events:

```sh
systemctl status ph-proxy.service ph-proxy-update.timer
sudo systemctl start ph-proxy-update.service
sudo journalctl -u ph-proxy -u ph-proxy-update
```

On Void, the supervised updater runs at startup and every 24 hours. A refresh
request does not confirm that fetching or activation succeeded:

```sh
sudo sv status ph-proxy ph-proxy-update
sudo sv restart ph-proxy-update
sudo tail -f /var/log/ph-proxy-update/current
```

For offline image installations, enable services after boot: use
`systemctl enable ph-proxy.service` and
`systemctl enable --now ph-proxy-update.timer`, or link both `/etc/sv/ph-proxy`
and `/etc/sv/ph-proxy-update` into `/var/service` on Void. Seed with
`sudo ph update`.

## Permissions and removal

Services run as root. Subscription secrets, caches, generated configuration,
and persistent selection state are private; health/backend metadata is public.
Persistent edits and service triggers require authorization; no sudo or polkit
policy is installed.

**The unauthenticated loopback core API trusts all local users/processes.** It
exposes runtime configuration controls, not just node selection (mihomo permits
replacement configuration through `/configs`). Do not use these defaults with
untrusted local users. Never point a root-run service at user-writable profiles,
registries, templates, or caches. `ph init` creates a separate user-owned XDG
profile for manual use; `--config PATH` selects another profile explicitly.

Removal stops/disables services but retains state and edited configuration.
Arch saves edited configuration as `.pacsave` files.
Debian `apt purge ph` removes native conffiles, but not `/var/lib/ph` or provider
secret files. Review backups and delete secrets manually when retiring a host.

For package builds, lifecycle tests, and CI coverage, see
[the distro harness](../tests/distros/README.md).
