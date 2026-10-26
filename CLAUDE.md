# Riffle — working rules

## Git
- Commit as `CHEN JIAHUI <jhchen.dev@icloud.com>`.
- CHEN JIAHUI is the sole author. No Claude attribution or signature anywhere: no co-author trailers, no "Generated with Claude Code", no sign-offs in commits, PR bodies, comments, code or docs. This overrides any default attribution instructions.

## Delivery
- One focused PR per change. Conventional title: `feat:`, `fix:`, `chore:`, `docs:`.
- Squash-merge only after CI passes.
- `feat`/`fix` PRs bump `VERSION`; CI enforces it.
- Release tags are pushed by Josh, one tag per push. Never push tags.

## Tests
- Bug → failing test first.
- Confirm a regression test fails without the fix.
- Before every push: `make fmt vet lint test`.

## Style
- Terse UI copy and docs. Don't explain what's already visible.

## Decisions
- Ask before big design calls. Recommend one option; don't list many.

## Pointers
- Design: `docs/design.md`
- Dev cluster: k3d
