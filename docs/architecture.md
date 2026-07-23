# Architecture

## One active Agent

Every command resolves exactly one Agent before creating a `service.Manager`. An explicit `--agent` is command-local. Without it, `internal/agent` checks configured executables in the stable order Codex then Claude.

The TUI switches the complete Manager context. It never combines Agent inventories. A completed TUI switch writes only the watcher target in `runtime.json`; foreground CLI selection never writes that target.

## Desired and local state

`config.yaml` is version 2 portable desired state. Its Codex and Claude sections independently own command, default state, active profile, and selectors. Profiles can explicitly select every Agent-supported state, including `manual`, so changing one Agent's default does not reinterpret another Agent's policy.

Recovery data is machine-local and split into `codex.json` and `claude.json`. `runtime.json` contains only the watcher target. Runtime accepts only these final formats; old data is backed up and manually converted outside the program.

## Adapter boundary

`internal/adapter.Adapter` owns Agent-specific discovery, supported states, prepare, apply, restore, delete, and close behavior. `service.Manager` owns resolution, personal/project gating, desired-state reconciliation, partial result reporting, orphans, and separate journal persistence.

Both adapters emit only personal and project Skills. Plugin, system, admin, and unknown scopes never enter the shared inventory.

## Codex

Filesystem roots are the ownership source:

- personal: `~/.agents/skills` and `~/.codex/skills`;
- project: ancestor `.agents/skills` roots from the repository root through the current directory.

The app-server is used only for `config/read` and `skills/config/write` on supported absolute paths. If enabled state cannot be verified, inventory is withheld and reconciliation fails rather than presenting or writing a guessed state. `implicit` and `manual` update only `policy.allow_implicit_invocation` in `agents/openai.yaml`; `disabled` changes enabled state. Plugin APIs and caches are not queried.

## Claude

Claude discovery reads `~/.claude/skills` plus ancestor `.claude/skills` roots. Personal definitions win same-name collisions over project definitions; the repository-root project definition wins over deeper ancestor definitions. Shadowed definitions remain visible and read-only.

Effective `skillOverrides` precedence is project-local, project-shared, user, then default `on`. Personal changes write user settings. Project changes require explicit project mode and write only `.claude/settings.local.json`.

State mapping is:

- `implicit` -> `on`
- `name-only` -> `name-only`
- `manual` -> `user-invocable-only`
- `disabled` -> `off`

Settings are decoded as structured JSON and only the selected override key is changed. Recovery compares that key only. An originally absent key is restored by deletion. Managed Settings are detected and reported as unsupported rather than included in effective-state calculation.

## Watcher

The watcher reads its Agent from `runtime.json`, lets an active synchronous reconciliation finish, then observes a changed target on a dedicated fast target poll and immediately reconciles the new Agent. Fingerprints contain only the selected Agent's configuration, journal, supported Skill inputs, and Claude settings inputs. Partial conflicts are printed as incomplete and retried without being reported as success.
