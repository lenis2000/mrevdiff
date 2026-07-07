package format

import (
	"bytes"
	"strings"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// IndentOptions controls the space.indent rule.
type IndentOptions struct {
	// Enabled gates the whole pass; when false, applyIndent is a no-op.
	Enabled bool
	// UseTab selects '\t' as the per-level indent character; otherwise ' '.
	UseTab bool
	// Size is the number of chars emitted per nesting level. Default 1 for
	// tab, 2 for space (resolved by the caller).
	Size int
	// ExtraNoIndentEnvs adds caller-supplied environments to the no-indent
	// list (in addition to the built-in defaults — currently `document`).
	ExtraNoIndentEnvs []string
	// Rules holds per-environment indent overrides. Key is the environment
	// name. Value is the literal indent string to use per nesting level:
	// empty string means no indent (like document), non-empty is the exact
	// string repeated per depth. When an env is present in Rules, it
	// overrides UseTab/Size for lines inside that env.
	Rules map[string]string
}

// noIndentEnvs are environments whose contents are NOT indented relative to
// the surrounding scope. `document` is the universal one; nothing else is
// hardcoded. Tier-2 / user lists can extend this via IndentOptions.
var noIndentEnvs = map[string]bool{
	"document": true,
}

func registerIndentRule() {
	Registry = append(Registry, Rule{
		ID:    "space.indent",
		Tier:  Safe,
		Doc:   "Re-indent lines based on environment nesting depth.",
		Apply: applyIndent,
	})
}

// indentEvent records a begin/end env event on a particular line.
type indentEvent struct {
	line    int
	envName string
	isBegin bool // true for \begin, false for \end
}

func applyIndent(ctx *Ctx) Result {
	if !ctx.Indent.Enabled {
		return Result{Src: ctx.Src}
	}

	// Count real content lines. parser.LineOffsets returns one entry per
	// actual line PLUS a trailing entry just past the last \n; so
	// len(Lines)-2 is the real line count when src ends with \n, and
	// len(Lines)-1 when it doesn't.
	nLines := bytes.Count(ctx.Src, []byte{'\n'})
	if len(ctx.Src) > 0 && ctx.Src[len(ctx.Src)-1] != '\n' {
		nLines++
	}
	if nLines <= 0 {
		return Result{Src: ctx.Src}
	}

	noIndentExtra := make(map[string]bool, len(ctx.Indent.ExtraNoIndentEnvs))
	for _, e := range ctx.Indent.ExtraNoIndentEnvs {
		noIndentExtra[e] = true
	}

	// skipEnv reports whether this env contributes zero indent and is
	// invisible to the stack (document, user-specified no-indent envs).
	skipEnv := func(env string) bool {
		return noIndentEnvs[env] || noIndentExtra[env]
	}

	// Collect events per line (ordered by token appearance).
	var events []indentEvent
	for _, tk := range ctx.Tokens {
		if tk.Line < 1 || tk.Line > nLines {
			continue
		}
		switch tk.Kind {
		case parser.TokBeginEnv:
			if skipEnv(tk.EnvName) {
				continue
			}
			off := tokenByteOffset(ctx.Lines, tk)
			if off < 0 || parser.OverlapsProtected(off, off+1, ctx.Protected) {
				continue
			}
			events = append(events, indentEvent{line: tk.Line, envName: tk.EnvName, isBegin: true})
		case parser.TokEndEnv:
			if skipEnv(tk.EnvName) {
				continue
			}
			off := tokenByteOffset(ctx.Lines, tk)
			if off < 0 || parser.OverlapsProtected(off, off+1, ctx.Protected) {
				continue
			}
			events = append(events, indentEvent{line: tk.Line, envName: tk.EnvName, isBegin: false})
		}
	}

	// Global indent unit (used when no per-env rule matches).
	indentChar := byte(' ')
	if ctx.Indent.UseTab {
		indentChar = '\t'
	}
	size := ctx.Indent.Size
	if size <= 0 {
		if ctx.Indent.UseTab {
			size = 1
		} else {
			size = 2
		}
	}
	globalUnit := strings.Repeat(string(indentChar), size)

	// indentUnit returns the indent string for one nesting level inside env.
	// Per-env rules override: "" means no indent, non-empty is the literal unit.
	hasRules := len(ctx.Indent.Rules) > 0
	indentUnit := func(env string) string {
		if hasRules {
			if unit, ok := ctx.Indent.Rules[env]; ok {
				return unit
			}
		}
		return globalUnit
	}

	// Build per-line ordered event lists preserving token order.
	// This is critical for same-line \begin{X}\end{X} pairs: the stack
	// update must process events in document order so that a begin
	// followed by a matching end nets to zero.
	type envEvent struct {
		envName string
		isBegin bool
	}
	type lineEvents struct {
		ordered []envEvent
	}
	lineEvt := make(map[int]*lineEvents)
	for _, ev := range events {
		le, ok := lineEvt[ev.line]
		if !ok {
			le = &lineEvents{}
			lineEvt[ev.line] = le
		}
		le.ordered = append(le.ordered, envEvent{envName: ev.envName, isBegin: ev.isBegin})
	}

	var out bytes.Buffer
	out.Grow(len(ctx.Src))
	changed := false
	var hits []Hit

	// envStack tracks the stack of currently-open environments.
	var envStack []string

	for line := 1; line <= nLines; line++ {
		body := lineBytes(ctx, line)

		// Collect events for this line.
		le := lineEvt[line]

		// Honour skip masks and protected lines: emit verbatim, do not
		// reindent. The token-level scan above already excluded any
		// protected/masked tokens from depth, so depth math stays
		// consistent.
		if ctx.LineSkipped(line) || lineWhollyProtected(ctx, line) {
			out.Write(body)
		} else {
			// Effective stack for this line: process \end events that
			// precede \begin events (reduce depth for the current line).
			// We pop only the leading \end events (those that appear
			// before the first \begin on this line), since a \begin on
			// this line opens an env whose body starts on the next line.
			viewStack := make([]string, len(envStack))
			copy(viewStack, envStack)
			if le != nil {
				for _, ev := range le.ordered {
					if ev.isBegin {
						break // stop at first \begin
					}
					// Pop the most recent matching env (inside-out).
					for i := len(viewStack) - 1; i >= 0; i-- {
						if viewStack[i] == ev.envName {
							viewStack = append(viewStack[:i], viewStack[i+1:]...)
							break
						}
					}
				}
			}

			leadLen, allWS := leadingWS(body)
			if allWS {
				// Blank line: preserve as-is (don't synthesise a phantom
				// indent on empty lines — common house style).
				out.Write(body)
			} else {
				// Build the wanted indent string from the env stack.
				var wantBuf strings.Builder
				for _, env := range viewStack {
					wantBuf.WriteString(indentUnit(env))
				}
				want := wantBuf.String()

				if string(body[:leadLen]) == want {
					out.Write(body)
				} else {
					out.WriteString(want)
					out.Write(body[leadLen:])
					changed = true
					hits = append(hits, Hit{
						RuleID:  "space.indent",
						Line:    line,
						Excerpt: truncExcerpt(string(body[leadLen:])),
					})
				}
			}
		}

		// Update the real env stack in token order: process each event
		// sequentially so that same-line \begin{X}\end{X} pairs balance.
		if le != nil {
			for _, ev := range le.ordered {
				if ev.isBegin {
					envStack = append(envStack, ev.envName)
				} else {
					for i := len(envStack) - 1; i >= 0; i-- {
						if envStack[i] == ev.envName {
							envStack = append(envStack[:i], envStack[i+1:]...)
							break
						}
					}
				}
			}
		}

		if line < nLines || endsWithNewline(ctx.Src) {
			out.WriteByte('\n')
		}
	}

	if !changed {
		return Result{Src: ctx.Src}
	}
	return Result{Src: out.Bytes(), Hits: hits}
}

// lineBytes returns the bytes of 1-based line L without the trailing newline.
func lineBytes(ctx *Ctx, line int) []byte {
	if line < 1 || line >= len(ctx.Lines) {
		return nil
	}
	start := ctx.Lines[line]
	if start > len(ctx.Src) {
		return nil
	}
	var end int
	if line+1 < len(ctx.Lines) {
		end = ctx.Lines[line+1]
		if end > 0 && end <= len(ctx.Src) && ctx.Src[end-1] == '\n' {
			end-- // strip trailing \n on full lines
		}
	} else {
		end = len(ctx.Src)
		if end > 0 && ctx.Src[end-1] == '\n' {
			end--
		}
	}
	if end < start {
		end = start
	}
	return ctx.Src[start:end]
}

// leadingWS returns the byte length of the leading whitespace prefix and
// whether the entire line is whitespace.
func leadingWS(line []byte) (int, bool) {
	i := 0
	for ; i < len(line); i++ {
		c := line[i]
		if c != ' ' && c != '\t' {
			return i, false
		}
	}
	return i, true
}

// lineWhollyProtected reports whether every byte of line lies inside one of
// ctx.Protected (verbatim/listing body, comment env). Comment-line spans
// only cover the comment portion, so a line with code-then-comment is NOT
// wholly protected and is still re-indented.
func lineWhollyProtected(ctx *Ctx, line int) bool {
	if line < 1 || line >= len(ctx.Lines) {
		return false
	}
	start := ctx.Lines[line]
	var end int
	if line+1 < len(ctx.Lines) {
		end = ctx.Lines[line+1]
		if end > 0 && end <= len(ctx.Src) && ctx.Src[end-1] == '\n' {
			end--
		}
	} else {
		end = len(ctx.Src)
	}
	if end <= start {
		return false
	}
	for _, sp := range ctx.Protected {
		if sp.Start <= start && end <= sp.End {
			return sp.Kind != "comment-line"
		}
	}
	return false
}

func endsWithNewline(src []byte) bool {
	return len(src) > 0 && src[len(src)-1] == '\n'
}
