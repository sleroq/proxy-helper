# Setup and use

For managed installations, start with [Linux packages](linux.md) or
[Nix modules](nix.md). Run persistent changes as the state owner: root for
managed profiles, your account for standalone profiles. `ph help` lists commands.

## Standalone Linux or macOS

Install Go 1.26+ and sing-box. From the repository:

```sh
go install ./cmd/ph
ph init
ph subscription add <<'JSON'
{"id":"main","url_file":"/absolute/private/provider-url","prefix":"main-"}
JSON
ph update
ph run
```

Provision the URL file privately, with mode 0600. Do not put credentials in
shell history. `subscription add` reads one JSON object from stdin.

- Config: `$XDG_CONFIG_HOME/ph/config.json`, or `~/.config/ph/config.json`.
- Proxy: `127.0.0.1:2080`. Control API: `127.0.0.1:9090`. No TUN or root required.
- Use `ph --config PATH COMMAND` for a separate profile. On a managed host,
  always pass this path and choose unused listener ports.
- Stop and rerun `ph run` after updates. For automatic activation, set
  `restart_command` to your service manager's argv array.
  Its service must launch `ph run`, not `ph prepare`. Commands do not use a shell.
- To start with mihomo, install that core and use `ph init --backend mihomo`.

## Daily commands

```sh
ph update                         # Fetch, validate, install, restart.
ph status
ph list
ph test                           # Measure the automatic group.
ph use                            # Interactive live selection.
ph use 'main-node'                # Live selection only.
ph pin 'main-node'                # Save selection and select it live.
ph unpin
ph subscription disable main      # Stage an edit.
ph subscription auto main --exclude-tag 'expired|quota'
ph apply                          # Validate cached nodes, install, restart.
```

- `list`, `test`, and `use` need the running API.
  Picker keys: `/` filters; arrows or `j/k` move; Enter selects; Esc or Ctrl+C exits.
- Pins persist even if live selection fails. A missing pinned node falls back
  to the generated default without deleting the pin.
- Automatic filters use Go regexes. Exclusions win; manual choices remain available.
  Disabling a source removes all its nodes but keeps its cache.
- Store and policy edits are staged. `apply` uses cache; `update` also fetches.
  `prepare` installs from cache without restarting.
- A partial refresh can install healthy sources plus cached nodes, then exit nonzero.
  If every fetch fails or validation fails, the installed snapshot stays unchanged.
- A restart failure after update/apply leaves the new config installed.
  Check the service; it may still run the old config.

## Switch clients

Managed Linux packages include both cores. Nix profiles must enable both.
Run these as the state owner:

```sh
ph backend list
ph backend select mihomo
ph backend select sing-box
```

Omit the name for an interactive picker. Switching briefly interrupts traffic.
See [client limits](backend-capabilities.md) before switching.

For standalone switching, add a `backends` map to your config:

```json
{
  "backends": {
    "sing-box": {"template_file": "template.json", "binary": "/absolute/path/to/sing-box"},
    "mihomo": {"template_file": "mihomo.json", "binary": "/absolute/path/to/mihomo"}
  },
  "restart_command": ["systemctl", "--user", "restart", "ph.service"]
}
```

This is a config fragment, not a complete profile. Supply each client's native
template with matching loopback proxy/API ports. Use absolute binary paths.
Configure your own user service to run `ph --config PATH run`; the restart
command must target that service. On macOS, use your user launchd job instead.
Switching needs this restart command and a working API.

## Stores and modes

- Config paths are relative to the config file. A relative `url_file` is
  relative to its subscription store.
- Source IDs must match `[A-Za-z0-9_-]+` and be unique. Tags must also be unique;
  use source prefixes to avoid collisions.
- Stores declare `name`, `path`, and `writable`. Each contains a
  `subscriptions` array of source objects. With multiple writable stores,
  `subscription add --store NAME` is required.
- Read-only stores are edited at their source. CLI overrides change policy,
  not URLs or identity. `subscription reset ID` restores declared policy.
- Sing-box `bypass on|off` toggles direct routing and local DNS inside the core.
  `tunnel off|on` removes or restores the template's TUN inbound.
  Both persist and require a restart command. Neither changes firewall policy.

## Security

- Keep stores, caches, generated configs, and URL files private (0600).
  Keep state writable only by its owner. Never feed user-writable files to root services.
- Public manifests expose source IDs, tags, server addresses, and health.
  Do not put secrets in names.
- The unauthenticated loopback API exposes runtime configuration controls,
  not just selection. Every local process is trusted to control the core.
- `ph config` redaction is best-effort. `ph check` prints raw core diagnostics.
  Review both before sharing.
- Bypass and tunnel-off are not kill switches. Host fail-closed routing needs
  separately managed firewall rules.
