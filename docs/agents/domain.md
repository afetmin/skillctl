# Domain Docs

This repository uses a single domain context. Agent identity is a dimension of the model, while each operation resolves one complete Agent context. CLI, TUI, and watcher share the same vocabulary for inventory, policy, synchronization, restoration, and Agent switching.

## Before exploring

- Read `CONTEXT.md` at the repository root when it exists.
- Read ADRs under `docs/adr/` that affect the area being changed.
- If these files do not exist, proceed silently. Do not require them to be created before work begins.

The `/domain-modeling` flow creates or updates the context and ADR files when terminology or architectural decisions are resolved.

## Layout

The intended layout is:

```text
/
|-- CONTEXT.md
`-- docs/adr/
```

Do not introduce `CONTEXT-MAP.md` or per-module contexts solely because another presentation layer is added. Move to a multi-context layout only when the repository contains independently evolving domain models.

## Vocabulary

Use terms as defined in `CONTEXT.md` in issue titles, implementation plans, tests, and documentation. Avoid synonyms that the glossary explicitly rejects.

If a required concept is missing, reconsider whether new terminology is necessary or note the gap for `/domain-modeling`.

## ADR conflicts

When proposed work conflicts with an existing ADR, identify the conflict explicitly rather than silently overriding the decision.
