# ADR 0002: Separate Agent Contexts and Manage Only Personal and Project Skills

## Status

Accepted

## Context

skillctl needs to support Codex and Claude, whose invocation controls and settings layers differ. Combining their inventories or retaining plugin lifecycle behavior would make ownership, effective state, recovery, and watcher behavior ambiguous.

This project currently has one user, so permanent compatibility logic for the previous single-Agent and plugin-aware formats has more cost than value.

## Decision

- Resolve one active Agent per operation and keep Codex and Claude inventories, profiles, reports, and journals separate.
- Detect installed commands in Codex-then-Claude order unless the current command explicitly selects an Agent.
- Manage only personal and project Skill filesystem roots.
- Exclude plugin, system, admin, and unknown scopes before inventory construction.
- Keep project Skills read-only unless project mode is explicit.
- Give Claude the additional `name-only` state and map all four states through `skillOverrides`.
- Preserve Claude settings with structured, field-level writes and recovery.
- Persist watcher target separately and let only completed TUI switches change it.
- Accept only version 2 configuration and journals. Back up and manually convert the one existing installation.

## Consequences

- ADR 0001's plugin inventory authority is no longer used.
- Codex app-server usage is limited to enabled-state reads and writes for supported paths.
- Claude Managed Settings remain an explicit verification limitation.
- Equal Skill names across Agents cannot collide because no operation combines Agent contexts.
- Partial synchronization can apply valid changes while reporting skipped conflicts with a non-zero result.
- Reintroducing plugin management would require a new product decision and adapter contract rather than cache scanning in shared discovery.
