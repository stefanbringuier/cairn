package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestKeepFlags(t *testing.T) {
	got, err := keepFlags("8, 24h,7d,4w,12m,2y")
	want := []string{"--keep-last", "8", "--keep-hourly", "24", "--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "12", "--keep-yearly", "2"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "7x", "d", "0d", "-1d", "0"} {
		if _, err := keepFlags(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestInitScopePreservesDefaultsAndRequiresExplicitTransitions(t *testing.T) {
	dataOnly, machine := true, false
	for _, tc := range []struct {
		name                     string
		scope                    string
		requested                *bool
		paths                    []string
		existing                 bool
		wantScope                string
		wantSnapshot, wantBackup []string
		wantErr                  bool
	}{
		{"new machine", "", nil, nil, false, scopeMachine, []string{home}, []string{home, "/etc", "/usr/share/keyrings"}, false},
		{"legacy paths", "", nil, []string{"/data"}, false, scopeMachine, []string{"/data"}, []string{"/data", "/etc", "/usr/share/keyrings"}, false},
		{"new data home", "", &dataOnly, nil, false, scopeDataOnly, []string{home}, []string{home}, false},
		{"new data paths", "", &dataOnly, []string{"/data", "/photos"}, false, scopeDataOnly, []string{"/data", "/photos"}, []string{"/data", "/photos"}, false},
		{"preserve data", scopeDataOnly, nil, nil, true, scopeDataOnly, []string{home}, []string{home, "/etc", "/usr/share/keyrings"}, false},
		{"data new sources", scopeDataOnly, nil, []string{"/data"}, true, scopeDataOnly, []string{"/data"}, []string{"/data"}, false},
		{"same scope", scopeDataOnly, &dataOnly, nil, true, scopeDataOnly, []string{home}, []string{home, "/etc", "/usr/share/keyrings"}, false},
		{"machine to data refused", scopeMachine, &dataOnly, nil, true, "", nil, nil, true},
		{"data to machine refused", scopeDataOnly, &machine, nil, true, "", nil, nil, true},
		{"machine to data", scopeMachine, &dataOnly, []string{"/data"}, true, scopeDataOnly, []string{"/data"}, []string{"/data"}, false},
		{"data to machine", scopeDataOnly, &machine, []string{"/data"}, true, scopeMachine, []string{"/data"}, []string{"/data", "/etc", "/usr/share/keyrings"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := defaultConfig()
			c.BackupScope = tc.scope
			c.Snapshot.Every, c.Backup.Keep, c.Exclude = "off", "9d", []string{"*.iso"}
			before, _ := json.Marshal(c)
			err := c.initScope(tc.requested, tc.paths, tc.existing)
			if tc.wantErr {
				after, _ := json.Marshal(c)
				if err == nil || string(before) != string(after) {
					t.Fatalf("unsafe transition changed settings: %v", err)
				}
				return
			}
			if err != nil || c.BackupScope != tc.wantScope || !slices.Equal(c.Snapshot.Paths, tc.wantSnapshot) || !slices.Equal(c.Backup.Paths, tc.wantBackup) {
				t.Fatalf("scope/paths: %+v, %v", c, err)
			}
			if c.Snapshot.Every != "off" || c.Backup.Keep != "9d" || !slices.Equal(c.Exclude, []string{"*.iso"}) {
				t.Fatal("scope reset schedules, retention, or excludes")
			}
		})
	}
}

func TestLoadBackupScopeAndExcludeStaleManifests(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state[old]"))
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ field, want string }{
		{"", scopeMachine}, {`,"backup_scope":"data-only"`, scopeDataOnly}, {`,"backup_scope":"typo"`, ""},
	} {
		fixture := `{"target":"/repo","repo":"/repo","host":"demo","snapshot":{"every":"1h","keep":"24h","paths":["/home/demo"]},"backup":{"every":"daily","keep":"7d","paths":["/home/demo"]}`
		if err := os.WriteFile(configFile(), []byte(fixture+tc.field+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := loadConfig()
		if tc.want == "" {
			if err == nil {
				t.Fatal("invalid scope accepted")
			}
			continue
		}
		if err != nil || c.BackupScope != tc.want {
			t.Fatalf("scope %q: %+v, %v", tc.field, c, err)
		}
		excludes, err := c.excludeArgs()
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Contains(excludes, globEscape(manifestDir())); got != (tc.want == scopeDataOnly) {
			t.Fatalf("manifest exclusion: %v", excludes)
		}
	}
}

func TestEditListAndSet(t *testing.T) {
	l := editList([]string{"a"}, "+b", strings.ToUpper)
	if l = editList(l, "-a", func(s string) string { return s }); !slices.Equal(l, []string{"B"}) {
		t.Errorf("edit: %v", l)
	}
	c := defaultConfig()
	if c.set("backup.keep", "nope") == nil || c.set("bogus", "x") == nil || c.set("snapshot.paths", "-"+home) == nil {
		t.Error("bad sets should fail")
	}
}
