package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	if got := readNames(managers[0], manifestDir()); !slices.Equal(got, []string{"git", "libc6:i386", "g++"}) {
		t.Errorf("apt names: %v", got)
	}
}

func TestRebuildUsesChosenRecipeWithoutStaleLiveManifests(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(manifestDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"apt.txt", "npm.txt", "crontab.txt"} {
		if err := os.WriteFile(filepath.Join(manifestDir(), file), []byte("stale-live-entry\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := scriptedRestic(t, `while [ "$#" -gt 0 ]; do
  case "$1" in
    --target) target="$2"; shift ;;
    --) shift; source="$1"; break ;;
  esac
  shift
done
[ "$source" = "chosen-machine:/home/old/.local/state/cairn/manifests" ] || exit 99
printf 'chosen-package\n' > "$target/apt.txt"
`)
	stage := filepath.Join(stateDir(), "rebuild")
	private, err := restoreRebuildManifests(r, Snap{ID: "chosen-machine"}, "/home/old/.local/state/cairn/manifests", stage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(private, stage+string(os.PathSeparator)) || private == manifestDir() {
		t.Fatalf("recipe not isolated: %s", private)
	}
	if got := readNames(managers[0], private); !slices.Equal(got, []string{"chosen-package"}) {
		t.Fatalf("chosen recipe not used: %v", got)
	}
	if len(readNames(managers[7], private)) != 0 || len(readNames(managers[0], "")) != 0 {
		t.Fatal("live package lists supplemented an absent recipe")
	}
	if _, err := os.Stat(filepath.Join(private, "crontab.txt")); !os.IsNotExist(err) {
		t.Fatal("live crontab entered chosen recipe")
	}
	failure := scriptedRestic(t, "exit 3\n")
	if dir, err := restoreRebuildManifests(failure, Snap{ID: "chosen-machine"}, "/home/old/.local/state/cairn/manifests", stage); err == nil || dir != "" {
		t.Fatalf("failed recipe made available: %q, %v", dir, err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(private) {
		t.Fatalf("failed recipe left partial lists: %v, %v", entries, err)
	}
}

func TestRebuildRefusesDataOnlyBeforeRepositoryAccess(t *testing.T) {
	c := Config{BackupScope: scopeDataOnly, Repo: "invalid"}
	err := cmdRebuild(c, "old-host", "/old-home", true)
	if err == nil || !strings.Contains(err.Error(), "data-only") || !strings.Contains(err.Error(), "recover") {
		t.Fatalf("data-only refusal: %v", err)
	}
}

func TestRebuildCandidatesExcludeDataOnlyAcrossScopeChanges(t *testing.T) {
	now := time.Now()
	all := []Snap{
		{ID: "machine", Time: now.Add(-time.Hour), Tags: []string{"backup"}},
		{ID: "data-backup", Time: now.Add(-time.Minute), Tags: []string{"backup", scopeDataOnly}},
		{ID: "snapshot", Time: now.Add(-time.Minute), Tags: []string{"snapshot"}},
		{ID: "new-machine", Time: now.Add(time.Minute), Tags: []string{"backup"}},
	}
	got := rebuildCandidates(Config{BackupScope: scopeMachine, Since: now}, all)
	if len(got) != 2 || got[0].ID != "machine" || got[1].ID != "snapshot" {
		t.Fatalf("rebuild sources: %+v", got)
	}
}
