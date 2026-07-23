# skillctl Domain Context

## Purpose

skillctl presents and manages invocation policy for one selected coding Agent at a time. It supports Codex and Claude personal and project Skills without taking ownership of plugins, bundled system content, or administrator policy.

## Glossary

- **Agent**: A supported Skill runtime, currently Codex or Claude.
- **Active Agent**: The single Agent selected for the current command or TUI context.
- **Skill**: A personal or project instruction package discovered from a supported filesystem root.
- **Personal Skill**: A user-owned Skill available across projects.
- **Project Skill**: A repository-context Skill; read-only unless project mode is explicit.
- **Invocation state**: `implicit`, `manual`, or `disabled` for Codex; Claude additionally supports `name-only`.
- **Desired profile**: One Agent's independent default and explicit selector lists.
- **Effective state**: The Agent-native state currently in effect, including Claude settings precedence.
- **Shadowed Skill**: A same-named Claude definition that loses precedence and is visible but read-only.
- **Recovery journal**: Agent-specific machine-local snapshots used for field-level restore.
- **Watcher target**: The Agent persisted in runtime state for background reconciliation.
- **Orphan**: Retained desired or recovery state whose supported Skill is currently absent.

## Invariants

- One operation has exactly one active Agent.
- Codex and Claude inventories, desired profiles, recovery journals, and sync results are never merged.
- Only personal and project scopes enter inventory.
- Plugin, system, admin, managed, and unknown Skill scopes are never modified.
- Project mutation requires explicit project mode.
- An explicit CLI Agent selection is temporary.
- Only a completed TUI switch changes the watcher target.
- Claude writes personal overrides to user settings and project overrides to project-local settings.
- Restore never overwrites an externally changed managed field.
- Deletion removes only a selected supported directory or directory symlink and retains desired and recovery state.

## Architecture Decisions

- [ADR 0001: Use Codex installed-plugin state as the Skill inventory authority](docs/adr/0001-codex-installed-plugin-state-authority.md) (superseded)
- [ADR 0002: Separate Agent contexts and manage only personal and project Skills](docs/adr/0002-agent-specific-personal-project-skills.md)
