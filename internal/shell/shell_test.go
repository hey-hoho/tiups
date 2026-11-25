package shell

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"/root/.tiup": `'/root/.tiup'`,
		"a b":         `'a b'`,
		"it's":        `'it'\''s'`,
		"$HOME;rm -x": `'$HOME;rm -x'`,
		"":            `''`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestInitSnippet(t *testing.T) {
	for _, want := range []string{`"$1" = "backup"`, "command tiup-backup", `command tiup "$@"`} {
		if !strings.Contains(InitSnippet, want) {
			t.Errorf("snippet lacks %q", want)
		}
	}
}
