package store

import "strings"

// SplitSQLStatements splits a SQL script into individual statements on
// semicolons, ignoring semicolons inside single-quoted string literals and
// on `--` line comments, and inside Postgres dollar-quoted bodies ($$ ... $$
// or $tag$ ... $tag$, as used by DO blocks). It does not handle `/* ... */`
// block comments; the migration files in this codebase do not use them.
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

		if c == '$' {
			if tag := dollarQuoteTag(script, i); tag != "" {
				end := strings.Index(script[i+len(tag):], tag)
				if end < 0 {
					// Unterminated body: keep the rest verbatim so the
					// database reports the syntax error.
					b.WriteString(script[i:])
					break
				}
				stop := i + len(tag) + end + len(tag)
				b.WriteString(script[i:stop])
				i = stop - 1
				continue
			}
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

// dollarQuoteTag returns the opening dollar-quote tag ($$ or $name$) that
// starts at script[i], or "" when script[i] does not open one. A '$' that
// follows an identifier character (e.g. a column named a$b) or is followed by a
// digit (a $1 placeholder) is not a quote.
func dollarQuoteTag(script string, i int) string {
	if i > 0 && isIdentByte(script[i-1]) {
		return ""
	}
	j := i + 1
	for j < len(script) && script[j] != '$' {
		c := script[j]
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		digit := j > i+1 && c >= '0' && c <= '9'
		if !letter && !digit {
			return ""
		}
		j++
	}
	if j >= len(script) {
		return ""
	}
	return script[i : j+1]
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
