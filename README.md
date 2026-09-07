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
Adjust the template and add a service manager separately if desired. Set
`restart_command` to an argv array to activate updates automatically.

Use an existing private URL file instead of typing credentials into shell history.
Source JSON also accepts `url`, for private stores managed outside Nix. `add`
reads exactly one source object from stdin and never accepts URLs as CLI arguments.

## Daily commands

```sh
sb subscription list
sb update                         # all enabled sources; all-or-nothing fetching
sb subscription update main       # same as sb update main
sb list
sb test                           # default automatic group
sb test auto-main
sb use 'a node tag'
sb status
sb config                         # best-effort redaction; file permissions apply
sudo sb config --raw               # explicitly root-only
sb check
```

`list`, `status`, `test`, and `use` need the running Clash API. `list`/`status`
read only a public manifest, not credentials. `subscription list` reads the actual
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

## Nix integration

The flake exports `packages.<system>.default` and `overlays.default` (`pkgs.sb`).
It builds the CLI and runs the subprocess integration suite. The dotfiles module
generates a non-secret config exposed at `/etc/sb/config.json` and wraps `sb` with
its immutable config path. It keeps DNS/TUN/routing, services, timers, capabilities,
and secret provisioning in Nix. The CLI owns runtime data and composition.

The system state directory is `/var/lib/sing-box`; mutations require its owner's
permissions (`sudo sb ...`). Existing declared `subscription.sources` become a
read-only store. `subscription.stores` adds agenix-managed complete stores or
editable files; its default is the private local store in the state directory.
Never put plaintext `url` values in generated Nix files. Nix store entries are public.

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
