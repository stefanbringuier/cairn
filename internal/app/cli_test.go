package app

import (
	"errors"
	"testing"
)

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
