package main

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
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

func TestSchedules(t *testing.T) {
	for every, want := range map[string]string{"15m": "*:0/15", "1h": "hourly", "hourly": "hourly", "12h": "0/12:00", "daily": "daily", "1w": "weekly", "off": ""} {
		if got, err := onCalendar(every); err != nil || got != want {
			t.Errorf("%s: got %q %v", every, got, err)
		}
	}
	if _, err := onCalendar("7m"); err == nil {
		t.Error("7m should fail")
	}
	if interval("12h") != 12*time.Hour || interval("off") != 0 {
		t.Error("interval")
	}
	c := defaultConfig()
	if err := c.set("snapshot.every", "hourly"); err != nil || c.Snapshot.Every != "1h" {
		t.Errorf("aliases must normalize to a TUI choice, got %q", c.Snapshot.Every)
	}
}

func TestRepoForAndRedact(t *testing.T) {
	for in, want := range map[string]string{"/mnt/nas/c": "/mnt/nas/c", "~/b": home + "/b", "sftp:u@h:/x": "sftp:u@h:/x", "rclone:g:x": "rclone:g:x"} {
		if got, err := repoFor(in); err != nil || got != want {
			t.Errorf("%s: got %q %v", in, got, err)
		}
	}
	if got := redact("rest:https://me:s3cret@host:8000/repo"); strings.Contains(got, "s3cret") || !strings.Contains(got, "me@host") {
		t.Errorf("redact: %s", got)
	}
	if redact("/mnt/nas") != "/mnt/nas" {
		t.Error("paths must pass through")
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

// Downloads the pinned restic and checks hash + that it runs; network only when asked.
func TestFetchRestic(t *testing.T) {
	if os.Getenv("CAIRN_NET_TEST") == "" {
		t.Skip("set CAIRN_NET_TEST=1")
	}
	dest := t.TempDir() + "/restic"
	if err := download("restic", dest); err != nil {
		t.Fatal(err)
	}
	if !resticOK(dest) {
		t.Fatal("downloaded restic doesn't run")
	}
}
