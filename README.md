# ph

Manage proxy subscriptions and switch clients on Linux and macOS.
Use Nix modules, native Linux packages, or a standalone CLI.

```text
Subscriptions -> ph -> sing-box / mihomo -> Proxy traffic
```

- Refresh subscriptions on a schedule with managed services.
- Keep working cached nodes when a source fails.
- Select nodes automatically, pick one live, or pin a favorite.
- Validate client configs before installing them.
- Switch clients through one managed service.

ph controls proxy cores. It is not a proxy or service supervisor.

## Get started

- [Native Linux packages](docs/linux.md) — Debian, Arch, and Void.
- [NixOS and nix-darwin](docs/nix.md) — shared modules and scheduled updates.
- [Standalone setup and daily use](docs/usage.md) — no Nix required.
- [Client support and limits](docs/backend-capabilities.md).

Managed profiles use a local HTTP/SOCKS proxy by default.
They do not reroute the host or provide a firewall kill switch.
The loopback control API trusts all local processes. Use only on trusted hosts.
