package app

import (
	"os"
	"path/filepath"
	"testing"
)

func scriptedRestic(t *testing.T, script string) Restic {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "restic")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nset -eu\n"+script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o700); err != nil { // #nosec G302 -- executable isolated restic fixture
		t.Fatal(err)
	}
	return Restic{bin: bin}
}

func TestRecoverNeverClobbersRacingDestinations(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "failed-export", "success", "restore-race"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, "candidate")
			original := filepath.Join(dir, "current-work")
			if err := os.WriteFile(original, []byte("current work"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CAIRN_TEST_DEST", destination)
			t.Setenv("CAIRN_TEST_ORIGINAL", original)
			t.Setenv("CAIRN_TEST_KIND", kind)
			r := scriptedRestic(t, `while [ "$#" -gt 0 ]; do
  if [ "$1" = --target ]; then target=$2; break; fi
  shift
done
if [ "$CAIRN_TEST_KIND" = restore-race ]; then
  printf 'saved copy' > "$target/current-work"
else
  printf 'saved copy' > "$target"
fi
case "$CAIRN_TEST_KIND" in
  file|restore-race) printf 'newer work' > "$CAIRN_TEST_DEST" ;;
  directory) mkdir "$CAIRN_TEST_DEST" ;;
  symlink) ln -s "$CAIRN_TEST_ORIGINAL" "$CAIRN_TEST_DEST" ;;
  failed-export) exit 1 ;;
esac
`)
			var err error
			if kind == "restore-race" {
				err = restoreSaved(r, Snap{ID: "snapshot"}, Node{Type: "file"}, original, destination, "")
			} else {
				err = recoverCopy(r, Snap{ID: "snapshot"}, Node{Type: "file"}, original, destination)
			}
			if (err == nil) != (kind == "success") {
				t.Fatalf("%s: %v", kind, err)
			}
			if data, _ := os.ReadFile(original); string(data) != "current work" { // #nosec G304 -- isolated test fixture
				t.Fatal("recovery modified current work")
			}
			if kind == "file" || kind == "restore-race" {
				if data, _ := os.ReadFile(destination); string(data) != "newer work" { // #nosec G304 -- isolated test fixture
					t.Fatal("recovery replaced a racing file")
				}
			}
			if kind == "failed-export" {
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatal("failed export published a partial file")
				}
			}
			if kind == "success" {
				if data, _ := os.ReadFile(destination); string(data) != "saved copy" { // #nosec G304 -- isolated test fixture
					t.Fatal("recovery did not publish the saved copy")
				}
			}
			if stages, _ := filepath.Glob(filepath.Join(dir, ".cairn-recover-*")); len(stages) != 0 {
				t.Fatal("recovery left staging files")
			}
		})
	}
}
