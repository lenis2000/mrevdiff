# CLAUDE.md

mrevdiff is a standalone, self-contained LaTeX review tool: a semantic
diff-review TUI (default) plus a source formatter/linter (`mrevdiff fmt`).
Behavior is akin to revdiff (`../revdiff`, UX reference only) with
edit-in-place and PDF build awareness. It was peeled out of the now-
deprecated mreview; **mrevdiff must not depend on, reference, or rely on
mreview in any way** (no `mreview/` imports, no `MREVIEW_*` env vars, no
`mreview` strings). Keep it fully independent.

## Architecture

- `cmd/mrevdiff` — CLI. `main.go` dispatches subcommands: `fmt` →
  `runFmt` (formatter), everything else → `runDiff` (diff review, incl.
  the bare `mrevdiff paper.tex` = `--base HEAD` form and `--version`).
- `pkg/diffreview` — endpoint resolution/materialization, semantic
  alignment, pair IDs, diff sidecars, stdout emit.
- `pkg/diffui` — the diff Bubble Tea model: outline/source/PDF panes,
  new-only editing, external compare (`C`, `MREVDIFF_COMPARE_EDITOR`),
  PDF reload, Q-discard, blink comparator, full-page PDF, command palette.
- `pkg/format` — the LaTeX formatter/linter rule engine (safe/pdf-fix/
  math rules, verify, the `.fmt-report.md` writer+reader). Header is
  `# mrevdiff fmt report`; source opt-out is `% mrevdiff-fmt: skip/off/on`.
- `pkg/ui` — small config/style/caps shim (TOML config incl. `[fmt]`,
  kitty detection, ParseShellArgs, lmkf protocol, external-issues loader).
- `pkg/parser`, `pkg/persist`, `pkg/build`, `pkg/pdf`, `pkg/synctex`.

## Rules

- **No mreview.** Do not reintroduce `mreview` imports, env-var fallbacks,
  header strings, or references. mrevdiff stands entirely alone.
- Git blob endpoints are read with `git show` and materialized under
  `.mrevdiff/`. Do not checkout, switch branches, commit, push, or mutate
  git refs from the tool.
- Editing must only write `Review.New.Path`, and only when
  `--allow-modifications` is set and the new endpoint is a real file.
- The lmkf wire protocol (`/tmp/lmkf-status/<project>`, log marker
  "Here is how much of TeX") is an external contract — never rename it.
  When lmkf watches a paper, the tool must not run its own latexmk.
- `S` opens Skim forward-search only; it must never trigger compilation.
- The `pdfverify` build tag gates the paranoid pixel verifier (needs
  `diff-pdf`); the default tag-less build uses the stub.
- Run `make install` (installs to `~/bin/mrevdiff`) as the final step of
  any code change; the user runs the installed binary.

## Tests

```
go test ./...
```
