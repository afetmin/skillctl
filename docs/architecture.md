# Architecture

## Desired state and local state

`config.yaml` is the portable desired state. It contains stable Skill IDs and policy lists but no machine paths.

`state.json` is the local recovery journal. It records the original policy and enabled state before the first mutation, plus the last hash written by `skillctl`. Keeping these files separate prevents machine-specific plugin cache paths from leaking into a dotfiles-friendly configuration.

## Codex adapter

Discovery prefers the local Codex app-server:

1. Start `codex app-server --listen stdio://`.
2. Complete the JSON-RPC initialize handshake.
3. Call `skills/list` with `forceReload: true`.
4. Use the returned absolute path, scope, name, and enabled state.

The adapter merges this authoritative list with a filesystem inventory of user roots and plugin caches. This lets `skillctl` preconfigure cached plugin Skills without enabling plugins that are currently inactive. When app-server discovery is unavailable, the adapter falls back to the filesystem inventory alone. Enabling and disabling still requires app-server because Codex owns that configuration.

## Policy reconciliation

The reconciler computes one desired state for every managed Skill:

1. A matching `disabled` selector wins.
2. A matching `implicit` selector enables implicit invocation.
3. Everything else uses the global `manual` default.

System and admin Skills are skipped. Repository Skills are skipped unless the caller explicitly enables project management.

For `implicit` and `manual`, the adapter ensures the Skill is enabled and changes only `policy.allow_implicit_invocation`. For `disabled`, it calls `skills/config/write` with the Skill's absolute path.

## Adapter boundary

The user-facing model is platform-neutral: discovery, stable IDs, the three invocation states, desired profiles, reconciliation reports, and restore journals. Codex-specific JSON-RPC and `agents/openai.yaml` behavior lives under `internal/codex` and `internal/policy`. Future Agent adapters can map the same three states to their native controls.
