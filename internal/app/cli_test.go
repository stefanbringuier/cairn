package app

import (
	"errors"
	"flag"
	"slices"
	"testing"
)

func TestParseInterspersedFlagsAndLiteralPaths(t *testing.T) {
	for _, tc := range []struct {
		args []string
		pos  []string
		to   string
		yes  bool
	}{
		{[]string{"-y", "file", "--to", "copy"}, []string{"file"}, "copy", true},
		{[]string{"file", "--to=copy", "-y"}, []string{"file"}, "copy", true},
		{[]string{"--to", "--", "file", "--json=false"}, []string{"file"}, "--", false},
		{[]string{"--to=-name", "-"}, []string{"-"}, "-name", false},
		{[]string{"--", "-y", "--to", "literal"}, []string{"-y", "--to", "literal"}, "", false},
		{[]string{"file", "--", "-y", "--json"}, []string{"file", "-y", "--json"}, "", false},
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		yes, to := fs.Bool("y", false, ""), fs.String("to", "", "")
		fs.Bool("json", false, "")
		if pos := parse(fs, tc.args); !slices.Equal(pos, tc.pos) || *to != tc.to || *yes != tc.yes {
			t.Errorf("%q: pos=%q to=%q yes=%v", tc.args, pos, *to, *yes)
		}
	}
}

func TestEscaping(t *testing.T) {
	if got := globEscape(`/a/[x]*?\b`); got != `/a/\[x\]\*\?\\b` {
		t.Errorf("glob: %s", got)
	}
	if clean("ok\x1b[31mred\x07") != "ok[31mred" {
		t.Error("control chars must be stripped")
	}
	if unitSafe("/home/u/my bin/cairn") != true || unitSafe("/x/%h/cairn") || unitSafe("/x\nExecStartPre=/evil") {
		t.Error("unitSafe")
	}
	if codeOf(nil) != 0 || codeOf(&resticError{code: 10}) != 10 || codeOf(errors.New("x")) != 1 {
		t.Error("codeOf")
	}
}
