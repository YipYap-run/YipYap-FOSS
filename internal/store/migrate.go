package store

import "strings"

// SplitSQLStatements splits a SQL script into individual statements on
// semicolons, ignoring semicolons inside single-quoted string literals and
// on `--` line comments. It does not handle `/* ... */` block comments or
// dollar-quoted strings; the migration files in this codebase do not use
// either.
func SplitSQLStatements(script string) []string {
	var stmts []string
	var b strings.Builder
	inString := false
	inLineComment := false

	for i := 0; i < len(script); i++ {
		c := script[i]

		if inLineComment {
			b.WriteByte(c)
			if c == '\n' {
				inLineComment = false
			}
			continue
		}

		if inString {
			b.WriteByte(c)
			if c == '\'' {
				// Escaped single quote (''), stay inside the literal.
				if i+1 < len(script) && script[i+1] == '\'' {
					b.WriteByte(script[i+1])
					i++
					continue
				}
				inString = false
			}
			continue
		}

		switch {
		case c == '\'':
			inString = true
			b.WriteByte(c)
		case c == '-' && i+1 < len(script) && script[i+1] == '-':
			inLineComment = true
			b.WriteByte(c)
			b.WriteByte(script[i+1])
			i++
		case c == ';':
			if stmt := strings.TrimSpace(b.String()); stmt != "" {
				stmts = append(stmts, stmt)
			}
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}

	if stmt := strings.TrimSpace(b.String()); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}
