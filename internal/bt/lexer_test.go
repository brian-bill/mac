package bt

import "testing"

// collect drains a lexer into a slice, stopping after the first EOF (which is
// included).
func collect(src string) []Token {
	l := NewLexer("test.bt", []byte(src))
	var toks []Token
	for {
		t := l.Next()
		toks = append(toks, t)
		if t.Kind == EOF {
			return toks
		}
	}
}

func TestNext_TokenStream(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Token
	}{
		{
			name: "metadata line",
			src:  "bpm: 120",
			want: []Token{
				{IDENT, "bpm", Position{1, 1}},
				{COLON, ":", Position{1, 4}},
				{INT, "120", Position{1, 6}},
				{EOF, "", Position{1, 9}},
			},
		},
		{
			name: "signature line",
			src:  "signature: 4/4",
			want: []Token{
				{IDENT, "signature", Position{1, 1}},
				{COLON, ":", Position{1, 10}},
				{INT, "4", Position{1, 12}},
				{SLASH, "/", Position{1, 13}},
				{INT, "4", Position{1, 14}},
				{EOF, "", Position{1, 15}},
			},
		},
		{
			name: "track header",
			src:  "[Kick]",
			want: []Token{
				{LBRACKET, "[", Position{1, 1}},
				{IDENT, "Kick", Position{1, 2}},
				{RBRACKET, "]", Position{1, 6}},
				{EOF, "", Position{1, 7}},
			},
		},
		{
			name: "sequence with dots and multi-digit hit",
			src:  "1 . 10 A",
			want: []Token{
				{INT, "1", Position{1, 1}},
				{DOT, ".", Position{1, 3}},
				{INT, "10", Position{1, 5}},
				{IDENT, "A", Position{1, 8}},
				{EOF, "", Position{1, 9}},
			},
		},
		{
			name: "trailing spaces produce no tokens before newline",
			src:  "1 .   \n2",
			want: []Token{
				{INT, "1", Position{1, 1}},
				{DOT, ".", Position{1, 3}},
				{NEWLINE, "\n", Position{1, 7}},
				{INT, "2", Position{2, 1}},
				{EOF, "", Position{2, 2}},
			},
		},
		{
			name: "CRLF normalizes to a single NEWLINE",
			src:  "a\r\nb",
			want: []Token{
				{IDENT, "a", Position{1, 1}},
				{NEWLINE, "\n", Position{1, 3}},
				{IDENT, "b", Position{2, 1}},
				{EOF, "", Position{2, 2}},
			},
		},
		{
			name: "illegal rune",
			src:  "#",
			want: []Token{
				{ILLEGAL, "#", Position{1, 1}},
				{EOF, "", Position{1, 2}},
			},
		},
		{
			name: "empty input is just EOF",
			src:  "",
			want: []Token{
				{EOF, "", Position{1, 1}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collect(tt.src)
			if len(got) != len(tt.want) {
				t.Fatalf("token count = %d, want %d\n got: %v\nwant: %v",
					len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("token[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNext_PositionTracking(t *testing.T) {
	// Line 2 begins after the newline; a leading tab counts as one column so
	// the IDENT on line 2 starts at column 2.
	src := "bpm: 120\n\tKick"
	got := collect(src)

	want := []Token{
		{IDENT, "bpm", Position{1, 1}},
		{COLON, ":", Position{1, 4}},
		{INT, "120", Position{1, 6}},
		{NEWLINE, "\n", Position{1, 9}},
		{IDENT, "Kick", Position{2, 2}},
		{EOF, "", Position{2, 6}},
	}
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestNext_EOFIsStable(t *testing.T) {
	l := NewLexer("test.bt", []byte("a"))
	_ = l.Next() // IDENT
	first := l.Next()
	second := l.Next()
	if first.Kind != EOF || second.Kind != EOF {
		t.Fatalf("expected repeated EOF, got %v then %v", first, second)
	}
}

func TestLoneCarriageReturnIsWhitespace(t *testing.T) {
	// A '\r' not followed by '\n' is treated as a separator, not a newline.
	got := collect("a\rb")
	want := []Token{
		{IDENT, "a", Position{1, 1}},
		{IDENT, "b", Position{1, 3}},
		{EOF, "", Position{1, 4}},
	}
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestTokenKindString(t *testing.T) {
	// Exercise the String methods used in diagnostics and test output.
	if got := ILLEGAL.String(); got != "ILLEGAL" {
		t.Errorf("ILLEGAL.String() = %q", got)
	}
	if got := TokenKind(99).String(); got != "TokenKind(99)" {
		t.Errorf("unknown kind String() = %q", got)
	}
	if got := (Position{4, 12}).String(); got != "4:12" {
		t.Errorf("Position.String() = %q, want 4:12", got)
	}
	if got := (Token{IDENT, "Kick", Position{4, 1}}).String(); got != `IDENT("Kick")@4:1` {
		t.Errorf("Token.String() = %q", got)
	}
}
