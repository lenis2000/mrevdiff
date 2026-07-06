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
| `J/K`, `pgdown/pgup` | jump 10 down / 5 up pairs |
| `f` | cycle filter (all / unreviewed / changed / …) |
| `m` | toggle semantic / coalesced diff regime |
| `z` | fold/unfold current outline group |
| `space` | mark pair reviewed |
| `a`, `ctrl+a`, `d` | annotate / edit / delete annotation |
| `y` | copy selected change (old/new side follows focus) |
| `e` / `E` | inline / external `$EDITOR` edit of the **new** file (needs `--allow-modifications`) |
| `u` / `ctrl+r` | undo / redo in-place edits |
| `B` | re-diff source + rebuild/reload PDF |
| `S` | Skim forward-search at current line (never compiles) |
| `P` | open new PDF in Preview |
| `C` | open old+new in external compare editor |
| `x` | blink comparator: flip the PDF pane between old and new builds (compiles the old side once, cached) |
| `/`, `n/N` | search pairs (source text, labels, pair IDs; respects the filter) |
| `@` | annotation list — enter jumps, `d` deletes |
| `i` | review scope popup (+ `--description` prose from an agent) |
| `[` / `]` | select previous/next source line (PDF anchor) |
| `h/l`, arrows | focus pane |
| `<` / `>` | resize focused pane / source split |
| `\` | cycle layout: full·PDF-side → full·PDF-below → sources+PDF (no outline) → new+PDF → sources-only |
| `\|` | PDF-only zoom (toggle; remembers the interrupted layout) |
| `?` | toggle in-app help (full-screen overlay) |
| `q` | quit, save sidecar, emit annotations |
| `Q Q` | discard annotations/marks and quit (in-place file edits stay) |

## PDF pane performance

The PDF pane ports the fast-rendering work from
[CLI-PDF-EPUB-reader](https://github.com/lenis2000/CLI-PDF-EPUB-reader):

- **2× supersampling on ghostty/agterm** — these terminals report logical
  pixels, so crops are rendered at doubled DPI (capped at 450) and
  downsampled by the terminal into crisp retina glyphs
  (`MREVDIFF_SUPERSAMPLE=1|2` overrides detection).
- **kitty `t=f` file transmission** — on local kitty/ghostty the escape
  carries only a file path (~200 bytes) instead of megabytes of base64
  through the PTY (`MREVDIFF_KITTY_XFER=file|direct` overrides; disabled
  automatically over SSH and under tmux).
- **Flicker-free frame swaps** — each frame transmits under its own image
  id; the previous frame is deleted *after* the new one paints, so the
  pane is never blank between blocks or across lmkf rebuilds. During a
  rebuild the last frame stays visible with progress in the status line.
- **Frame cache + neighbor prefetch** — rendered frames are memoised, and
  the blocks adjacent to the cursor are pre-rendered in the background so
  `j`/`k` navigation is instant.
- **BestSpeed PNG encoding** — frames are transient; encode latency is on
  the interactive path, size is not.
- **Torn-PDF guard** — a rebuilt PDF missing its `%%EOF` trailer (latexmk
  mid-write) is never opened; the previous document stays up and the tool
  retries.
- **Column-aware crops** — per-page column detection (median width of
  SyncTeX-mapped blocks) slices two-column papers (PNAS etc.) to the
  region's column instead of the full page width; single-column papers
  are unaffected. Blocks whose lines carry no SyncTeX records (e.g.
  boxed front-matter like `\significancestatement`) anchor to the
  nearest mapped line instead of showing a dead placeholder.
- **Region marker** — an amber outline inside every crop marks the exact
  SyncTeX region of the cursor block, so the eye lands on the changed
  lines instead of hunting through the context.
- **Blink comparator (`x`)** — the old endpoint compiles once into a
  content-addressed cache (`.mrevdiff/oldpdf-<hash>/`, reused across
  sessions) and `x` flips the pane between old and new renders at
  identical geometry; a changed subscript or shifted equation pops out
  the way a moving star pops out of a blink comparator.

## agterm integration

When mrevdiff runs inside [agterm](https://github.com/umputun/agterm)
(`AGTERM_ENABLED=1` and `agtermctl` on PATH), two hooks activate:

- **Session flag** — the session is flagged in agterm's flagged
  working-set view whenever the review carries pending annotations or the
  last rebuild failed, and unflagged when neither holds (and on quit).
  The sidebar becomes a "reviews that need me" list.
- **Overlay editing** — `E` opens `$EDITOR` in an agterm overlay on top
  of the session instead of suspending the TUI, so the PDF frame stays
  painted and the review is visible the moment the editor closes.

`MREVDIFF_AGTERM=0` disables both. Everywhere else they are no-ops.

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
`MREVDIFF_HISTORY_DIR`. For `MREVDIFF_THEME`, `MREVDIFF_FORCE_KITTY`, and
`MREVDIFF_COMPARE_EDITOR` the `MREVIEW_*` spellings are honoured as
fallbacks so an existing mreview setup keeps working; the exit-code and
history vars are new to mrevdiff and have no `MREVIEW_*` equivalent.

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
