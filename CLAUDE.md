# CLAUDE.md

mrevdiff is a standalone semantic diff review TUI for LaTeX, peeled out of
mreview's diff mode (`../mreview`). Behavior is akin to revdiff
(`../revdiff`, UX reference only) with edit-in-place and PDF build
awareness.

## Architecture

- `cmd/mrevdiff` — CLI (go-flags), endpoint wiring, TUI runner, history
  auto-save, exit-code-on-annotations. Supports `--base REV path.tex`,
  explicit `OLD NEW`, and the bare `mrevdiff paper.tex` form (= `--base HEAD`).
- `pkg/diffreview` — endpoint resolution/materialization, semantic
  alignment, pair IDs, diff sidecars, stdout emit.
- `pkg/diffui` — the diff Bubble Tea model: outline/source/PDF panes,
  new-only editing, external compare (`C`, `MREVDIFF_COMPARE_EDITOR`),
  PDF reload, Q-discard.
- `pkg/ui` — small shim (styles, TOML config, kitty detection,
  ParseShellArgs, lmkf protocol, external-issues loader). NOT the full
  mreview single-file UI.
- `pkg/parser`, `pkg/persist`, `pkg/build`, `pkg/pdf`, `pkg/synctex`,
  `pkg/format` — support packages copied from mreview.

## Rules

- Git blob endpoints are read with `git show` and materialized under
  `.mrevdiff/`. Do not checkout, switch branches, commit, push, or mutate
  git refs from the tool.
- Editing must only write `Review.New.Path`, and only when
  `--allow-modifications` is set and the new endpoint is a real file.
- The lmkf wire protocol (`/tmp/lmkf-status/<project>`, log marker
  "Here is how much of TeX") is an external contract — never rename it.
  When lmkf watches a paper, the tool must not run its own latexmk.
- `S` opens Skim forward-search only; it must never trigger compilation.
- The fmt-report reader keeps the literal `# mreview fmt report` header —
  those files are produced by `mreview fmt`.
- `MREVIEW_*` env vars are honoured as fallbacks for their `MREVDIFF_*`
  counterparts; keep that compatibility.
- Run `make install` (installs to `~/bin/mrevdiff`) as the final step of
  any code change; the user runs the installed binary.

## Tests

```
go test ./...
```
