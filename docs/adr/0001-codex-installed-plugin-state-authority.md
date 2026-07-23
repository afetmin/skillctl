# ADR 0001: Use Codex Installed-Plugin State as the Skill Inventory Authority

## Status

Superseded by [ADR 0002](0002-agent-specific-personal-project-skills.md)

## Context

Codex plugin cache directories can retain multiple versions, packages from old marketplaces, uninstalled plugins, and incomplete downloads. Scanning the complete cache therefore produces a historical file inventory rather than the set of Skills Codex currently uses. That can create duplicate plugin groups, incorrect invocation states, and drift for inactive packages.

skillctl must still expose an individually disabled Skill from an otherwise active plugin so that the user can enable it again. Codex may omit that Skill from `skills/list`, so authoritative plugin membership and limited filesystem supplementation are both required.

## Decision

Codex `plugin/installed` is the only authority for plugin membership, plugin-level enablement, and the active plugin version.

- A plugin contributes Skills only when Codex reports both `installed=true` and `enabled=true`.
- `skills/list` supplies the Skills Codex currently exposes.
- Filesystem discovery may supplement individually disabled Skills only within an active plugin package whose marketplace, plugin ID, and exact version all match the Codex response.
- skillctl never selects a different cached version as a fallback.
- Plugin discovery uses skillctl's current working directory so repository marketplaces match the active Codex context.
- Plugin-level disabled packages are hidden. Individually disabled Skills within active plugins remain visible and manageable.
- Historical caches are ignored but never deleted by skillctl.
- Policies and restoration records for removed plugins are retained and reported by doctor as orphaned.

When the app-server is unavailable or does not support `plugin/installed`:

- plugin caches are not scanned as a substitute authority;
- list and TUI may continue with non-plugin Skills and an explicit warning;
- sync and doctor stop with an error rather than operate on partial inventory;
- an explicitly targeted non-plugin `set` may continue when it can be resolved safely;
- plugin-targeted mutations require verified installation state.

## Consequences

- skillctl prefers a smaller, correct inventory over a larger inventory inferred from stale files.
- Old plugin versions and marketplace migrations no longer create duplicate active groups.
- Disabled Skills remain recoverable without treating every cache directory as installed.
- Reliable plugin management requires a Codex version that supports `plugin/installed`.
- Missing exact-version packages are reported as incomplete instead of being silently replaced.
- Cache cleanup remains Codex's responsibility.
- Active inventory and retained policy state are deliberately separate concepts.
