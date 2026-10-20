package config

import (
	"bytes"
	"io"
)

// newCommentStripReader returns a reader over JSON with // line comments
// removed, so a hand-edited config file may carry explanatory comments.
// Comment markers inside string literals are preserved.
//
// This is a deliberately small, allocation-light pass rather than a full
// JSON5 parser; it handles the one affordance we want (line comments) and
// nothing else.
func newCommentStripReader(raw []byte) io.Reader {
	out := make([]byte, 0, len(raw))
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		// Outside a string: detect "//" and skip to end of line.
		if c == '/' && i+1 < len(raw) && raw[i+1] == '/' {
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			if i < len(raw) {
				out = append(out, '\n')
			}
			continue
		}
		out = append(out, c)
	}
	return bytes.NewReader(out)
}
