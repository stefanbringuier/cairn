package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRestoreRefusesRepositoryAndActiveControlPaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	repo := filepath.Join(dir, "repository")
	for _, path := range []string{filepath.Join(repo, "config"), configFile(), passwordFile()} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("current control data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := scriptedRestic(t, "exit 99\n")
	for _, path := range []string{dir, repo, filepath.Join(repo, "config"), filepath.Join(repo, "snapshots/new"), configDir(), configFile(), passwordFile()} {
		if undo, err := saveBeforeRestore(r, Config{Repo: repo}, path); err == nil || undo != "" || strings.Contains(err.Error(), "exit 99") {
			t.Errorf("control path %s reached backup/restore: undo=%s err=%v", path, undo, err)
		}
	}
	if err := os.Remove(passwordFile()); err != nil {
		t.Fatal(err)
	}
	if _, err := saveBeforeRestore(r, Config{Repo: repo}, passwordFile()); err == nil {
		t.Fatal("missing active password destination accepted")
	}
}

func TestRestoreUsesFrozenDestination(t *testing.T) {
	dir := t.TempDir()
	container := filepath.Join(dir, "container")
	if err := os.Mkdir(container, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(container, "notes.txt")
	// The old container disappears while the safety copy is running.
	if err := os.Remove(container); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(container, []byte("new container-file work"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := scriptedRestic(t, `while [ "$#" -gt 0 ]; do
  if [ "$1" = --target ]; then mkdir -p "$2"; printf 'old version' > "$2/notes.txt"; exit; fi
  shift
done
exit 99
`)
	if err := restoreSaved(r, Snap{ID: "saved"}, Node{Type: "file"}, "/old/notes.txt", dest, "undo"); err == nil {
		t.Fatal("restore unexpectedly wrote through changed container")
	}
	if data, _ := os.ReadFile(container); string(data) != "new container-file work" { // #nosec G304 -- isolated test fixture
		t.Fatal("restore replaced a container that was not saved")
	}
}

func TestRestoreRequiresCompleteVerifiedSafetySnapshot(t *testing.T) {
	for _, kind := range []string{"partial", "invalid-json", "missing-id", "missing-path", "success"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recent-work.txt")
			if err := os.WriteFile(path, []byte("recent work"), 0o600); err != nil {
				t.Fatal(err)
			}
			node, err := json.Marshal(Node{Path: path, Type: "file"})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("CAIRN_TEST_PATH", path)
			t.Setenv("CAIRN_TEST_NODE", string(node))
			t.Setenv("CAIRN_TEST_KIND", kind)
			t.Setenv("CAIRN_TEST_ID", strings.Repeat("a", 64))
			r := scriptedRestic(t, `case " $* " in
  *" backup "*)
    case "$CAIRN_TEST_KIND" in
      partial) exit 3 ;;
      invalid-json) printf 'broken output\n' ;;
      missing-id) printf '{"message_type":"summary"}\n' ;;
      *) printf '{"message_type":"summary","snapshot_id":"%s"}\n' "$CAIRN_TEST_ID" ;;
    esac ;;
  *" ls "*)
    if [ "$CAIRN_TEST_KIND" != missing-path ]; then printf '%s\n' "$CAIRN_TEST_NODE"; fi ;;
  *" restore "*) printf 'old version' > "$CAIRN_TEST_PATH" ;;
esac
`)
			undo, err := restoreInPlace(r, Config{Host: "fixture"}, Snap{ID: "saved"}, Node{Type: "file"}, path)
			if kind == "success" {
				if err != nil || undo != strings.Repeat("a", 64) {
					t.Fatalf("complete snapshot failed: %s %v", undo, err)
				}
				if data, _ := os.ReadFile(path); string(data) != "old version" { // #nosec G304 -- isolated test fixture
					t.Fatal("successful restore did not replace file contents")
				}
			} else {
				if err == nil || undo != "" {
					t.Fatalf("unsafe restore accepted: %s %v", undo, err)
				}
				if data, _ := os.ReadFile(path); string(data) != "recent work" { // #nosec G304 -- isolated test fixture
					t.Fatal("unsafe restore changed current work")
				}
			}
		})
	}
}

func TestPickAndCandidates(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)
	snaps := []Snap{ // oldest first, like restic
		{ID: "aa11", Time: now.Add(-48 * time.Hour), Paths: []string{"/home/u"}, Tags: []string{"snapshot"}},
		{ID: "123d00", Time: now.Add(-2 * time.Hour), Paths: []string{"/data"}, Tags: []string{"backup"}},
		{ID: "cc33", Time: now.Add(-1 * time.Hour), Paths: []string{"/home/u"}, Tags: []string{"snapshot"}},
		{ID: "dd440", Time: now.Add(-1 * time.Minute), Paths: []string{"/home/u/x"}, Tags: []string{"pre-restore"}},
		{ID: "dd441", Time: now, Paths: []string{"/home/u/x"}, Tags: []string{"pre-restore"}},
	}
	if c, _, _ := pick(snaps, "3d", now); !c.Equal(now.Add(-72 * time.Hour)) {
		t.Errorf("3d: %v", c)
	}
	if c, _, _ := pick(snaps, "2026-10-01", now); c.Format("2006-01-02 15:04:05") != "2026-10-01 23:59:59" {
		t.Errorf("date: %v", c)
	}
	if _, id, _ := pick(snaps, "123d", now); id != "123d00" {
		t.Errorf("a hex id must win over an age: %q", id)
	}
	if _, _, err := pick(snaps, "dd44", now); err == nil {
		t.Error("ambiguous id prefix must fail")
	}
	if _, _, err := pick(snaps, "99999999d", now); err == nil {
		t.Error("absurd ages must fail, not overflow")
	}
	ids := func(s []Snap) (out []string) {
		for _, x := range s {
			out = append(out, x.ID)
		}
		return out
	}
	if got := ids(candidates(snaps, "/home/u/x", now, "")); !slices.Equal(got, []string{"cc33", "aa11"}) {
		t.Errorf("latest: %v", got)
	}
	if got := ids(candidates(snaps, "/home/u/x", now.Add(-24*time.Hour), "")); !slices.Equal(got, []string{"aa11"}) {
		t.Errorf("cutoff: %v", got)
	}
	if got := ids(candidates(snaps, "/home/u/x", now, "dd440")); !slices.Equal(got, []string{"dd440"}) {
		t.Errorf("by id: %v", got)
	}
	if (Snap{Paths: []string{"/home/u"}}).covers("/home/user2") || !(Snap{Paths: []string{"/"}}).covers("/etc") {
		t.Error("covers must match whole path components")
	}
}
