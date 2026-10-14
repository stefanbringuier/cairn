package app

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestInitRefusesInvalidExistingSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []byte(`{"backup_scope":"invalid"}`)
	if err := os.WriteFile(configFile(), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "unused-repository")
	err := cmdInit(target, false, []string{"/data"}, true, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown backup scope") {
		t.Fatalf("init ignored invalid settings: %v", err)
	}
	after, err := os.ReadFile(configFile())
	if err != nil || string(after) != string(bad) {
		t.Fatal("init overwrote invalid settings")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("init created a repository before checking settings")
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
