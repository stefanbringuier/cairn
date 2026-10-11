package app

import (
	"os"
	"slices"
	"testing"
)

func TestManifests(t *testing.T) {
	snap := managers[1].names("Name Version Rev\ncore22 1 1\nfirefox 1 1\nsnapd 1 1\n")
	cargo := managers[6].names("ripgrep v14.1.0:\n    rg\nbat v0.24.0:\n    bat\n")
	uv := managers[4].names("ruff v0.6.9\n- ruff\nmypy v1.0\n- mypy\n")
	npm := managers[7].names(`{"dependencies":{"npm":{},"@openai/codex":{},"typescript":{}}}`)
	if !slices.Equal(snap, []string{"firefox"}) || !slices.Equal(cargo, []string{"ripgrep", "bat"}) ||
		!slices.Equal(uv, []string{"ruff", "mypy"}) || !slices.Equal(npm, []string{"@openai/codex", "typescript"}) {
		t.Errorf("snap %v cargo %v uv %v npm %v", snap, cargo, uv, npm)
	}
	// Restored manifests feed `sudo apt install`: options and local files must never get through.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_ = os.MkdirAll(manifestDir(), 0o700)
	_ = os.WriteFile(manifestDir()+"/apt.txt", []byte("git\n-oDPkg::Pre-Invoke::=/x\n/home/u/evil.deb\nlibc6:i386\ng++\n"), 0o600)
	if got := readNames(managers[0]); !slices.Equal(got, []string{"git", "libc6:i386", "g++"}) {
		t.Errorf("apt names: %v", got)
	}
}
