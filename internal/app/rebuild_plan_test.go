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

func TestRebuildMappingSyntaxAndOnePassRewrite(t *testing.T) {
	for _, values := range [][]string{{"old=/new"}, {"/old=new"}, {"/old"}, {"/old=/new", "/old/./=/other"}} {
		if _, err := parseRebuildMappings(values); err == nil {
			t.Fatalf("accepted invalid mappings: %v", values)
		}
	}
	mappings, err := parseRebuildMappings([]string{"/old=/new", "/old/nested=/deep", "/new=/other"})
	if err != nil {
		t.Fatal(err)
	}
	original := Config{Snapshot: Job{Paths: []string{"/old/a", "/old/nested/a", "/oldish", "/new/a"}}, Backup: Job{Paths: []string{"/old"}}}
	updated := remapRebuildConfig(original, mappings)
	if !slices.Equal(updated.Snapshot.Paths, []string{"/new/a", "/deep/a", "/oldish", "/other/a"}) || updated.Backup.Paths[0] != "/new" {
		t.Fatalf("rewrite: %+v", updated)
	}
	if original.Snapshot.Paths[0] != "/old/a" || original.Backup.Paths[0] != "/old" {
		t.Fatal("mutated original config")
	}
}

func TestRebuildMappingPreflightUsesCanonicalDestinations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	oldHome := home
	home = filepath.Join(root, "home")
	t.Cleanup(func() { home = oldHome })
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	c := Config{Repo: filepath.Join(root, "repo")}
	for _, mappings := range [][]rebuildMapping{
		{{From: "/old/a", To: actual}, {From: "/old/b", To: filepath.Join(alias, "nested")}},
		{{From: "/old/a", To: filepath.Join(c.Repo, "data")}},
		{{From: "/old/a", To: filepath.Join(home, "data")}},
		{{From: "/old/a", To: filepath.Join(root, "destination")}, {From: root, To: filepath.Join(t.TempDir(), "outside")}},
	} {
		if err := validateRebuildMappings(c, mappings); err == nil {
			t.Fatalf("unsafe mappings accepted: %+v", mappings)
		}
	}
	mappings := []rebuildMapping{{From: "/old/a", To: filepath.Join(root, "missing", "data")}}
	if err := validateRebuildMappings(c, mappings); err != nil || !mappings[0].Missing {
		t.Fatalf("missing target: %+v %v", mappings, err)
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatal("preflight created destination parent")
	}
}

func TestRebuildSourcesNeverAdmitPostInitOrSafety(t *testing.T) {
	now := time.Now()
	c := Config{Since: now, BackupScope: scopeDataOnly}
	all := []Snap{
		{ID: "future", Time: now.Add(time.Hour), Tags: []string{"backup"}},
		{ID: "safety", Time: now.Add(-time.Hour), Tags: []string{"pre-restore"}},
		{ID: "mixed", Time: now.Add(-time.Hour), Tags: []string{"backup", "pre-restore"}},
		{ID: "data", Time: now.Add(-time.Hour), Tags: []string{"backup", scopeDataOnly}},
		{ID: "machine", Time: now.Add(-2 * time.Hour), Tags: []string{"backup"}},
	}
	got := rebuildSources(c, all)
	if len(got) != 2 || got[0].ID != "machine" || got[1].ID != "data" {
		t.Fatalf("eligible sources: %+v", got)
	}
	c.BackupScope = scopeMachine
	if got = rebuildSources(c, all); len(got) != 1 || got[0].ID != "machine" {
		t.Fatalf("machine sources: %+v", got)
	}
	if len(all) != 5 || all[0].ID != "future" {
		t.Fatal("mutated source list")
	}
}

func TestRebuildNonTTYRefusalDoesNotAcquireTools(t *testing.T) {
	if isTTY() {
		t.Skip("requires nonterminal test process")
	}
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("PATH", filepath.Join(root, "empty"))
	err := cmdRebuildMapped(Config{}, rebuildOptions{NoHome: true, Maps: []string{"/old=/new"}})
	if err == nil || reportError(err) != exitNeedsYes {
		t.Fatalf("refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Fatal("refusal acquired a tool")
	}
}

func TestExplicitSafetyReceiptIsExactAndRestoreOnly(t *testing.T) {
	id := strings.Repeat("a", 64)
	path := "/saved/exact"
	c := Config{Host: "new-host"}
	for _, test := range []struct {
		name      string
		snapshots []Snap
		at, path  string
		ok        bool
	}{
		{"valid", []Snap{{ID: id, Hostname: "old-host", Tags: []string{"pre-restore"}, Paths: []string{path}}}, id, path, true},
		{"wrong-path", []Snap{{ID: id, Tags: []string{"pre-restore"}, Paths: []string{path + "/other"}}}, id, path, false},
		{"ordinary", []Snap{{ID: id, Tags: []string{"backup"}, Paths: []string{path}}}, id, path, false},
		{"missing-tag", []Snap{{ID: id, Paths: []string{path}}}, id, path, false},
		{"extra-tag", []Snap{{ID: id, Tags: []string{"pre-restore", "backup"}, Paths: []string{path}}}, id, path, false},
		{"collision", []Snap{{ID: id, Tags: []string{"pre-restore"}, Paths: []string{path}}, {ID: id, Tags: []string{"pre-restore"}, Paths: []string{path}}}, id, path, false},
		{"short-id", []Snap{{ID: id, Tags: []string{"pre-restore"}, Paths: []string{path}}}, id[:8], path, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			blob, _ := json.Marshal(test.snapshots)
			t.Setenv("CAIRN_TEST_SAFETY_SNAPS", string(blob))
			t.Setenv("CAIRN_TEST_SAFETY_NODE", `{"path":"/saved/exact","type":"file"}`)
			r := scriptedRestic(t, `case " $* " in
 *" snapshots "*) case " $* " in *" --host "*) printf '[]';; *) printf '%s' "$CAIRN_TEST_SAFETY_SNAPS";; esac;;
 *" ls "*) printf '%s\n' "$CAIRN_TEST_SAFETY_NODE";;
 *) exit 97;;
esac
`)
			_, _, err := resolveRestore(r, c, test.path, test.at)
			if (err == nil) != test.ok {
				t.Fatalf("resolve: %v", err)
			}
			if test.ok {
				if _, _, err := resolve(r, c, path, id); err == nil {
					t.Fatal("shared recover/history resolver widened across hosts")
				}
			}
		})
	}
}

func TestReinitializeHostRefusalAndFailedConnectionPreserveIdentity(t *testing.T) {
	root := profileFixture(t)
	c := saveFixtureProfile(t, "default", filepath.Join(root, "repo"))
	c.Host = "previous-machine"
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile(), []byte("existing-password"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isTTY() {
		if err := cmdInit(c.Repo, false, nil, false, nil); err == nil || reportError(err) != exitNeedsYes {
			t.Fatalf("host-switch refusal: %v", err)
		}
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	script := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf 'restic 0.19.1\\n'; exit; fi\nexit 10\n")
	if err := os.WriteFile(filepath.Join(bin, "restic"), script, 0o700); err != nil {
		t.Fatal(err)
	} // #nosec G306 -- isolated executable fixture
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := cmdInit(c.Repo, false, nil, true, nil); err == nil {
		t.Fatal("failed repository access accepted")
	}
	after, err := os.ReadFile(configFile())
	if err != nil || string(after) != string(before) {
		t.Fatal("failed init changed host/config")
	}
	password, err := os.ReadFile(passwordFile())
	if err != nil || string(password) != "existing-password" {
		t.Fatal("failed init changed password")
	}
	if _, err := os.Stat(filepath.Join(root, "config", "systemd")); !os.IsNotExist(err) {
		t.Fatal("failed init wrote timers")
	}
}
