package ui

// ParseShellArgs tokenises an $EDITOR-style string the way a POSIX
// shell would for simple commands: whitespace separates tokens;
// single quotes take their contents literally; double quotes take
// contents literally except that a backslash before `"` or `\`
// is an escape; outside quotes a backslash escapes the next rune
// (so `\ ` produces a literal space). Unterminated quotes fall
// through as a single token ending at EOL — good enough for
// `EDITOR` strings, which are never interactively composed.
func ParseShellArgs(s string) []string {
	const (
		stateNormal = iota
		stateSingle
		stateDouble
	)
	var out []string
	var buf []rune
	state := stateNormal
	inToken := false
	flush := func() {
		if inToken {
			out = append(out, string(buf))
			buf = buf[:0]
			inToken = false
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch state {
		case stateNormal:
			switch {
			case r == ' ' || r == '\t':
				flush()
			case r == '\'':
				state = stateSingle
				inToken = true
			case r == '"':
				state = stateDouble
				inToken = true
			case r == '\\' && i+1 < len(runes):
				i++
				buf = append(buf, runes[i])
				inToken = true
			default:
				buf = append(buf, r)
				inToken = true
			}
		case stateSingle:
			if r == '\'' {
				state = stateNormal
			} else {
				buf = append(buf, r)
			}
		case stateDouble:
			if r == '"' {
				state = stateNormal
			} else if r == '\\' && i+1 < len(runes) &&
				(runes[i+1] == '"' || runes[i+1] == '\\') {
				i++
				buf = append(buf, runes[i])
			} else {
				buf = append(buf, r)
			}
		}
	}
	flush()
	return out
}
