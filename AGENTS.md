# Project rules

ph manages subscriptions and controls proxy clients. Keep it a short-lived
composer/controller; service managers own supervision.

## Ownership

- `cmd/ph`: arguments, terminal UI, and backend selection.
- `internal/app`: update, cache, policy, validation, installation, and activation.
  Keep these use cases backend-neutral.
- `internal/backend`: client adapters, commands, and live controllers.
- `subscription`: stores, source policy, fetching, and URI parsing.
- `proxy`: shared cached nodes. Their raw fields are sing-box-shaped,
  not a portable client config schema.
- `singbox` and `mihomo`: separate native config composers.
- `clash`: API transport, not shared routing or config semantics.

## Contracts to preserve

- Validate candidates with the target core before installation.
  Reject unsupported translations instead of dropping meaningful fields.
- Keep source failures isolated. Preserve last-good cache and report partial refreshes.
- Keep `use` live-only and `pin` durable. ph owns saved selection.
- Use argv arrays, never shell strings. Managed restart paths must run
  `ph run`, not `prepare`: backend switching holds the state lock during restart.
- Ordinary activation releases the lock before restart and does not roll back.
  Switching holds it through readiness checks and attempts rollback on failure.
- Files are replaced atomically, not as a multi-file transaction.
  One state directory must own each set of writable stores.
- Keep sensitive files 0600. Never log URLs, credentials, subscription bodies,
  or raw validator errors during updates.
- Keep managed inputs root-owned. Loopback API binding is not local-user authorization.
- Keep docs about current behavior and open work. Do not add implementation journals.

## Verification

```sh
go test -race -count=1 ./...
golangci-lint run
ast-grep scan --config "$HOME/sgconfig.yml" --no-ignore hidden .
```

CI also runs gopls, deadcode, and [native lifecycle tests](tests/distros/README.md).
Optional Go integration checks exercise native parsing and core validation using
absolute `PH_REAL_CORE` and `PH_REAL_MIHOMO` paths; `PH_REAL_TEMPLATE` supplies
a sing-box template.
Do not treat subprocess doubles as real-core or host-routing verification.
