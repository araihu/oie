# Oie contributor instructions

Read docs/oie-spec.md before changing behavior. Keep agreed requirements, recommendations, open decisions, and backlog distinct.

- Define HTTP contracts in api/openapi.yaml before implementing operations. Use vacuum and oapi-codegen; do not edit generated Go or generated CLI files by hand.
- Keep build/generation tools in the tools module; root go.mod contains application dependencies only. Vacuum remains isolated under tools/vacuum because of its incompatible templ tool dependency.
- Define SQLite queries in SQL and generate the query layer with sqlc. Use embedded golang-migrate up/down migrations, preserving existing data directories; do not hand-edit generated queries or silently force dirty migrations.
- Domain and application packages must not depend on HTTP, SQL, Goshtoso, or protocol client packages. Adapters implement application-owned ports.
- Reuse public Goshtoso components and examples. Generic dependency gaps belong upstream, in an open PR with a concrete commit pseudo-version pinned here. Do not import dependency internal/demo packages.
- Keep user-facing expressions localized in English and Brazilian Portuguese from the start.
- Use a managed worktree for feature implementation; preserve unrelated changes.
- Run make check and relevant integration checks before presenting changes. Document unavailable checks precisely.
- Milestone one is foundation work; do not claim live messaging, sync, or backlog features are implemented.
