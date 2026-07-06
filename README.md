# mrevdiff

Semantic diff review TUI for LaTeX papers — peeled out of
[mreview](https://github.com/lenis2000/mreview)'s diff mode into a
standalone tool with behavior akin to
[revdiff](https://github.com/umputun/revdiff), plus edit-in-place and
PDF build awareness.

Instead of diffing lines, mrevdiff parses both versions of a paper into
LaTeX blocks (sections, theorems, proofs, displays, figures, paragraphs)
and aligns them semantically: by `\label`, by stable block ID, by
normalized-source hash, and finally by fuzzy token match. You review
*change pairs*, not hunks — with the freshly built PDF crop for the
current block rendered next to the source via kitty graphics.

## Usage

```bash
mrevdiff paper.tex                     # review uncommitted changes (vs HEAD)
mrevdiff --base v1 paper.tex           # working tree vs tag/branch/rev
mrevdiff old.tex new.tex               # two explicit endpoints
mrevdiff HEAD~5:paper.tex paper.tex    # git blob vs working file
mrevdiff --allow-modifications --base HEAD paper.tex   # enable e/E edits
```

On quit (`q`), annotations are emitted to stdout as markdown (or
`--stdout=json|none`), so agents can consume the review:

```bash
mrevdiff --base HEAD paper.tex > review.md
```

Review state (annotations, reviewed marks, cursor) persists in a sidecar
file `<paper>.tex.mrevdiff.<rev>.md` next to the paper, so a review can
be resumed. `Q Q` quits discarding the session's changes (no sidecar
write, no emit). With `--exit-code-on-annotations`, an annotated quit
exits 10 (revdiff's launcher convention). Annotated reviews are also
snapshotted to `~/.config/mrevdiff/history/<project>/` as a safety net
(`--no-history` disables).

## Keys

| Key | Action |
|---|---|
| `j/k`, counts, `gg/G`, `{`/`}` | navigate pairs / sections |
| `f` | cycle filter (all / unreviewed / changed / …) |
| `m` | toggle semantic / coalesced diff regime |
| `space` | mark pair reviewed |
| `a`, `ctrl+a`, `d` | annotate / edit / delete annotation |
| `e` / `E` | inline / external `$EDITOR` edit of the **new** file (needs `--allow-modifications`) |
| `u` / `ctrl+r` | undo / redo in-place edits |
| `B` | re-diff source + rebuild/reload PDF |
| `S` | Skim forward-search at current line (never compiles) |
| `P` | open new PDF in Preview |
| `C` | open old+new in external compare editor |
| `\` | cycle PDF layout (side / below / hidden) |
| `q` | quit, save sidecar, emit annotations |
| `Q Q` | discard session changes and quit |

## PDF build awareness

mrevdiff never races the build pipeline: when
[lmkf](https://github.com/lenis2000) (`latexmk -pvc` wrapper) is already
watching the paper — detected via `/tmp/lmkf-status/<project>` — the tool
skips its own build entirely and waits for lmkf's pass to finish after
each edit. Without lmkf it runs `latexmk` itself (configurable with
`--build-cmd` / `build_cmd`; `--no-build` disables).

## Configuration

TOML config at `~/.config/mrevdiff/config.toml`, overridden by the
nearest `.mrevdiff.toml` up to the git root. Keys: `build_cmd`, `theme`
(`dark`/`light`), `theorem_envs`, `figure_envs`.

Environment: `MREVDIFF_THEME`, `MREVDIFF_FORCE_KITTY`,
`MREVDIFF_COMPARE_EDITOR` (for `C`; falls back to `opendiff`, `zed`),
`$EDITOR` (for `E`), `MREVDIFF_EXIT_CODE_ON_ANNOTATIONS`,
`MREVDIFF_HISTORY_DIR`. The `MREVIEW_*` spellings are honoured as
fallbacks so an existing mreview setup keeps working.

## Requirements

- `git` (endpoint resolution; git blobs are materialized read-only under
  `<repo>/.mrevdiff/`)
- kitty or ghostty for the inline PDF pane (other terminals get a
  text placeholder)
- `latexmk` unless lmkf is watching or `--no-build` is set
- macOS for the `S`/`P` external-viewer keys (Skim / Preview)

## Install

```bash
make install     # builds and installs to ~/bin/mrevdiff + zsh completion
```

## Development

```bash
make test        # go test -cover ./...
make lint        # go vet
```

The lint diagnostics surfaced in the outline (issue markers) are read
from `<paper>.tex.fmt-report.md` files produced by `mreview fmt` — the
two tools compose but neither requires the other.
