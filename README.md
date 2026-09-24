# sb

A small Go CLI for subscription management and sing-box control on Linux and
macOS. It is not a proxy, service supervisor, or replacement subscription parser.
The initial parser adapter uses `sing-box-sub` (rainbend/sing-box-subscribe-cli
v1.0.4); native configuration validation uses your installed `sing-box`.

## Standalone setup

Install Go 1.26+, sing-box, and the converter (named `sing-box-sub` on PATH), then:

```sh
go install ./cmd/sb
sb init
sb subscription add <<'JSON'
{"id":"main","url_file":"/absolute/private/subscription-url"}
JSON
sb update
sing-box run -c "$HOME/.config/sb/state/config.json"
```

Configuration defaults to `$XDG_CONFIG_HOME/sb/config.json`, falling back to
`~/.config/sb/config.json` on **both** macOS and Linux. `--config PATH` must precede
the command. `init` creates a native sing-box template with a loopback mixed proxy
on port 2080 and Clash API on port 9090; it does **not** enable TUN or require root.
Adjust the template and add a user service manager separately if desired. The
standalone files and generated config remain owned by your account; no Nix,
root access, system TUN, or platform-specific service manager is needed. Keep
the URL file and writable subscription store private (`chmod 600`), and keep
`~/.config/sb/state` writable only by your account. `sb update` installs a new
config; start or restart `sing-box run -c ~/.config/sb/state/config.json`
manually when there is no service. To automate activation, set
`restart_command` to an argv array for **your own** service manager (for
example, `systemctl --user restart sing-box.service` on Linux or a user
`launchctl kickstart -k gui/<UID>/<LABEL>` on macOS). Do not put it through a
shell or run the standalone setup as root.

Use an existing private URL file instead of typing credentials into shell history.
Source JSON also accepts `url`, for private stores managed outside Nix. `add`
reads exactly one source object from stdin and never accepts URLs as CLI arguments.

## Daily commands

```sh
sb subscription list
sb update                         # attempt all enabled sources; install viable mixed snapshot
sb subscription update main       # same as sb update main
sb list
sb test                           # default automatic group
sb test auto-main
sb use                           # live-only picker (requires stdin/stdout TTY)
sb use 'a node tag'              # live-only, scriptable
sb pin 'a node tag'              # persist a leaf and select it live
sb unpin                         # restore automatic/default selection
sb bypass on                     # direct routing + local DNS inside sing-box
sb bypass off                    # restore proxy routing and saved selection
sb tunnel off                    # stop TUN interception; keep mixed proxy running
sb tunnel on                     # restore TUN interception
sb status
sb config                         # best-effort redaction; file permissions apply
sudo sb config --raw               # explicitly root-only
sb check
```

`list`, `status`, `test`, and `use` need the running Clash API. `pin`/`unpin`
validate and install offline without restarting, then try to change the live
selector; if the API is unavailable, the saved configuration still takes effect
on the next start. In the picker, `/` filters by tag, source or server;
arrow keys or `j`/`k` move, Enter selects, Esc clears the filter or quits,
and Ctrl+C quits immediately. Latency measurements run in the background and
never delay opening or closing the picker. Pin intent is private
`state_dir/pin.json` (0600), writable only by the state directory owner; rootless `use` remains available when the
system state is root-owned. Only currently available leaf outbounds, including
manual subscription and static/extra leaves, can be pinned. Disabled/deleted
leaves leave a stale pin saved but fall back to the generated default. Public
manifest and `list`/`status` show pin intent/activity separately from live
selection. Legacy complete-outbounds mode does not support pin/unpin. `list`/`status`
read public `subscription.json` and `health.json`, not credentials. These 0644
files disclose source IDs, node counts, outbound tags (including static/extra
tags), timestamps, and generic fetch failures; extra outbound endpoints remain
private. Keep IDs free of secrets. A partial update installs healthy and last-good cached
sources, skips unavailable uncached sources, restarts, then exits nonzero. If no
fetch succeeds or validation fails, the installed snapshot is unchanged; fetch
health is still recorded. `prepare` can reuse unavailable entries when another
outbound remains selectable. `subscription list` reads the actual
private registry and therefore needs its owner's permissions (root on Nix).
`check` deliberately forwards core diagnostics to the caller; do not publish them
without reviewing for credentials.

## Stores and ownership

Example non-secret CLI configuration:

```json
{
  "template_file": "template.json",
  "state_dir": "state",
  "api_url": "http://127.0.0.1:9090",
  "sing_box": "sing-box",
  "converter": "sing-box-sub",
  "stores": [
    {"name":"declared", "path":"/run/agenix/sb-subscriptions", "writable":false},
    {"name":"local", "path":"subscriptions.json", "writable":true}
  ],
  "overrides_file": "overrides.json"
}
```

Each store has the same schema:

```json
{
  "subscriptions": [
    {
      "id": "main",
      "url_file": "/run/agenix/provider-url",
      "prefix": "main-",
      "disabled": false,
      "auto": true,
      "exclude_tags": ["expired", "quota"],
      "include_servers": ["\\.example\\.net$"]
    }
  ]
}
```

- IDs match `[A-Za-z0-9_-]+` and must be unique across stores. Outbound tags must
  also be unique; use prefixes when providers reuse names. Duplicate store names
  are rejected. A missing writable store is initially empty; missing read-only
  stores fail loudly.
- Config file paths are relative to the config directory; a relative `url_file`
  is relative to its subscription store. Executable names use PATH; Nix supplies
  absolute executable paths. No shell interprets commands.
- `subscription add --store local` chooses an owner. With multiple writable
  stores, `--store` is mandatory. `subscription delete ID` only edits writable
  stores. For read-only records, edit the declaring file or disable the record.
- Enable/disable and selection commands save narrow overrides, never URLs or
  source identity. `subscription reset ID` removes all overrides for that ID and
  restores declared policy. Store files can be editable symlinks: atomic writes
  preserve the symlink and replace its resolved target.
- CLI edits are **staged**. `sb apply` composes cached nodes, validates, installs,
  and restarts; `sb update` also fetches. This lets several edits be applied
  together. `sb prepare` is the offline, no-restart service-start operation.
  Deleted source caches are removed on the next successful apply/update.

## Automatic selection

```sh
sb subscription auto main --enabled false
sb subscription auto main --enabled true --exclude-tag 'test|expired'
sb subscription auto main --include-server '\.example\.net$'
sb subscription auto main --exclude-server '^slow\.' --exclude-server '^old\.'
sb subscription auto main --clear
sb subscription disable main
sb subscription enable main
sb subscription reset main
sb apply
```

Filters use Go regular expressions. Within an inclusion list, any pattern can
match; when both tag and server inclusion lists exist, both must match.
Exclusions win. A supplied filter flag replaces that list; repeat it to supply
several patterns. Unspecified lists are retained. `--clear` clears the effective
automatic policy; `reset` instead returns to the store's policy.

Automatic exclusion affects both `auto` and `auto-ID`, **not manual selection**.
Disabling a subscription removes its nodes entirely from the generated config
while retaining its cache. Empty URLTest groups are omitted. If all automatic
candidates are excluded, the selector defaults to its first manual node. If no
nodes remain at all, application fails rather than silently switching to direct.
Static/extra outbounds remain automatic candidates in this release.

## Bypass, interception, and fail-closed

`sb bypass on` routes traffic **entering sing-box** directly, including DNS
queries handled by sing-box. It renders a direct-only selector (no proxy nodes
or URLTest needed), a direct route final, and only a local DNS resolver;
remote DNS servers and DNS routing rules are omitted while bypassed so they
cannot send queries via a proxy detour. Custom templates that force unsupported outbound routes
are rejected rather than silently bypassed. `sb bypass off` restores the
normal proxy route and saved pin/automatic choice; without any proxy nodes it
fails and leaves bypass intact. Selecting a proxy with `sb use` does **not**
turn bypass off. This does not change the OS resolver or stop applications' own
DNS-over-HTTPS connections.

`sb tunnel off` instead removes recognized TUN inbounds from the generated
config and restarts sing-box; mixed proxy inbounds and their normal routing
remain. `sb tunnel on` restores the template's TUN. Standalone templates without
TUN cannot use this toggle. Both controls persist privately in
`state_dir/mode.json` (0600) through `prepare`, updates, and restarts; they
require the state owner's authorization and a configured `restart_command`.
A failed restart can leave the installed config different from the running
service; `status` shows requested/installed modes, **not verified host routing**.
On NixOS/macOS, use the authorized system service restart configured by the
platform module. Neither control stops the service or changes firewall policy.

**Neither bypass nor tunnel-off is a kill switch.** If sing-box fails or its TUN
is removed, the OS may send traffic directly. A real fail-closed mode requires
separately managed OS firewall rules on each host and is not implemented here.
Disabling subscriptions is not a way to disable transparent interception.

## Nix integration and refresh authorization

The flake exports `packages.<system>.default`, `overlays.default`,
`nixosModules.default`, and `darwinModules.default`. Import the appropriate
module and set `services.sb.enable = true`, `services.sb.subscription.enable =
true`, `services.sb.converterPackage` (the `sing-box-sub` package), and secret
`subscription.sources = [{ name = "main"; urlFile = "/run/agenix/provider-url"; }]`.
The default template is a loopback mixed proxy; configure a native TUN, DNS,
and routing with `services.sb.settings` only on hosts that need transparent
routing. The module generates a non-secret `/etc/sb/config.json` and an `sb`
wrapper that uses it. Existing root-owned `/var/lib/sing-box` state and legacy
caches remain in place; the service runs `prepare` on startup and the root-owned
update timer runs `sb update`. Do not switch services before reviewing generated
config and secret paths. Never put plaintext `url` values in Nix files.

For managed hosts, set `services.sb.refreshUser = "USERNAME"`. **`sb refresh`**
then requests a fixed, argument-free update without sudo: NixOS permits that
user via polkit to *start only* `sing-box-update.service`; the root unit fetches,
validates, installs, and restarts. nix-darwin gives that user write access only
to `/var/lib/sing-box/refresh-request` (0600) inside a root-owned directory;
launchd watches the file and runs the same fixed root update. The Darwin request
is asynchronous: `sb refresh` confirms the trigger was touched, **not** that
fetch or activation succeeded; inspect `sb status` and the service log. WatchPaths
can coalesce rapid requests, so this is not a synchronous RPC. `sb refresh ID`
is refused; selecting a source for a privileged update requires the owner to
run `sb update ID` directly. If `refreshUser` is unset, no user is granted a
refresh trigger. User accounts may still use read-only `sb status`, `sb list`,
`sb test`, and live-only `sb use` via the loopback Clash API.

The root-owned state directory, cache, overrides, local subscription store,
mode/pin files, and backend config are **not** user-writable; subscription URLs
stay in root-readable secret files. A user-writable registry or override in a
root-run process would allow source injection or symlink/path attacks. The only
managed user-write boundary is the Darwin request file, which the privileged
updater never opens or parses. The public 0644 manifest/health files reveal
source IDs, tags, server addresses, and outcomes but not credentials. Preserve
root ownership when migrating an existing installation; inspect any previously
editable symlinks before enabling the managed updater. Standalone user-owned
stores and caches are separate and must never be reused as managed root state.

The original `subscription-outbounds.json` + `subscription-sources.json` cache is
imported on first preparation/update without a fetch. Legacy `outboundsFile`
mode also uses `prepare`, with its complete outbound array preserved. Existing
legacy files are left intact; remove them manually after verifying migration.

## Reusable packages and boundaries

- `subscription`: source identity, policy, multi-store catalog, and converter
  adapter. `Converter.Parse(ctx, reader, source)` works with any subscription
  body; `Fetch` adds HTTP and URL-file access. Neither applies application policy.
- `singbox`: raw-JSON-preserving outbounds, pure `Compose`/`Legacy` config
  construction, and a small Clash API client. Unknown protocol fields and exact
  JSON numbers survive composition. No dependency on sing-box Go internals.
- `internal/app`: update/prepare/edit use cases, state, external validation and
  activation. `cmd/sb` owns arguments and terminal presentation.

There is no DI framework, generic repository layer, or alternate-backend hierarchy.
Public packages are intended for reuse but their APIs are pre-1.0. The module path
is `github.com/sleroq/sb`; until published, consume the local module using Go's
`replace` directive or a workspace.

## Failure and security boundaries

Fetch/conversion/composition/core-validation failures leave installed state
untouched. The updater holds an OS advisory lock and releases it **before**
restarting, since service startup calls `prepare`. A failed restart explicitly
reports that the new configuration has already been installed. No automatic
rollback is attempted.

Individual files are atomically replaced, but cache, manifest and config are
**not a multi-file transaction**; disk failure or interruption between replacements
can leave mixed metadata until the next successful prepare/update. One state
directory must own a set of writable stores; do not point independently running
profiles at the same mutable registry with different locks.

Secret stores, overrides, caches and config are written mode 0600. Conversion
uses a private temporary directory. HTTP/core/converter failures suppress raw
diagnostics that may contain credentials. The public manifest includes tags,
protocol types, server names and source IDs: metadata is not anonymous.
`config` redaction is best-effort and remains permission-protected, not a safe
public export API. HTTPS subscription URLs are recommended; HTTP is allowed for
private/local providers. The API client currently supports unauthenticated local
Clash endpoints only. Keep the controller loopback-bound.

## Verification

```sh
go test -race ./...
golangci-lint run --enable=modernize
nix build
```

Only integration/end-to-end tests are used: compiled CLI processes, real local
HTTP, filesystem permissions/symlinks, and subprocess doubles for the converter
and validator. Optional real-core coverage uses `SB_REAL_CORE` and
`SB_REAL_CONVERTER` absolute paths, with optional `SB_REAL_TEMPLATE` to validate
an existing non-secret native template. It does not contact real providers or start TUN.
