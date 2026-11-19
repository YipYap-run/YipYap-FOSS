package store

import "testing"

func TestSplitSQLStatements(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "multiple statements",
			input: "CREATE TABLE a (x TEXT);\nCREATE TABLE b (y TEXT);",
			want:  []string{"CREATE TABLE a (x TEXT)", "CREATE TABLE b (y TEXT)"},
		},
		{
			name:  "semicolon inside string literal",
			input: `INSERT INTO t (v) VALUES ('a;b'); INSERT INTO t (v) VALUES ('c');`,
			want:  []string{"INSERT INTO t (v) VALUES ('a;b')", "INSERT INTO t (v) VALUES ('c')"},
		},
		{
			name:  "line comment with semicolon",
			input: "-- note; still a comment\nCREATE TABLE a (x TEXT);",
			want:  []string{"-- note; still a comment\nCREATE TABLE a (x TEXT)"},
		},
		{
			name:  "escaped single quote",
			input: "INSERT INTO t (v) VALUES ('it''s; fine');",
			want:  []string{"INSERT INTO t (v) VALUES ('it''s; fine')"},
		},
		{
			name:  "trailing semicolon and whitespace",
			input: "CREATE TABLE a (x TEXT);\n\n",
			want:  []string{"CREATE TABLE a (x TEXT)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitSQLStatements(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("SplitSQLStatements() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("stmt[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
