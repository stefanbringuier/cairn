package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func profileFixture(t *testing.T) string {
	t.Helper()
	previous := selectedProfile
	selectedProfile = "default"
	t.Cleanup(func() { selectedProfile = previous })
	root := t.TempDir()
	for _, kind := range []string{"CONFIG", "STATE", "DATA", "CACHE"} {
		t.Setenv("XDG_"+kind+"_HOME", filepath.Join(root, strings.ToLower(kind)))
	}
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	return root
}

func saveFixtureProfile(t *testing.T, name, repo string) Config {
	t.Helper()
	previous := selectedProfile
	selectedProfile = name
	defer func() { selectedProfile = previous }()
	c := defaultConfig()
	c.Target, c.Repo = repo, repo
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProfileSelectionAndNames(t *testing.T) {
	for _, name := range []string{"default", "nas", "cloud-2", "0", strings.Repeat("a", 48)} {
		for _, args := range [][]string{{"--profile", name, "status"}, {"--profile=" + name, "status"}} {
			got, rest, err := selectProfile(args)
			if err != nil || got != name || !slices.Equal(rest, []string{"status"}) {
				t.Fatalf("selection %q: %s %q %v", args, got, rest, err)
			}
		}
	}
	for _, name := range []string{"", "../nas", "a/b", ".", "UPPER", "na_s", "-a", "a-", "a\n", strings.Repeat("a", 49)} {
		if _, _, err := selectProfile([]string{"--profile", name, "init"}); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	if _, _, err := selectProfile([]string{"--profile"}); err == nil {
		t.Fatal("missing name accepted")
	}
	args := []string{"recover", "--", "--profile=literal"}
	name, rest, err := selectProfile(args)
	if err != nil || name != "default" || !slices.Equal(rest, args) {
		t.Fatal("literal path interpreted as profile")
	}
	profileFixture(t)
	if Run([]string{"--profile", "nas", "--version"}, "test") != 0 || selectedProfile != "default" {
		t.Fatal("Run leaked profile selection")
	}
	if Run([]string{"--profile", "../outside", "init"}, "test") != exitNeedsYes || selectedProfile != "default" {
		t.Fatal("invalid profile did not fail before dispatch")
	}
}

func TestProfileFilesAndCredentialsStayIndependent(t *testing.T) {
	root := profileFixture(t)
	legacy := saveFixtureProfile(t, "default", filepath.Join(root, "legacy"))
	legacy.Snapshot.Keep = "50,24h"
	if err := legacy.save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile(), []byte("legacy password"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyBytes, _ := os.ReadFile(configFile())
	if configFile() != filepath.Join(root, "config/cairn/config.json") || stateDir() != filepath.Join(root, "state/cairn") || unitName("snapshot") != "cairn-snapshot" {
		t.Fatal("legacy layout changed")
	}
	saveFixtureProfile(t, "cloud", filepath.Join(root, "cloud"))
	selectedProfile = "cloud"
	if configFile() != filepath.Join(root, "config/cairn/profiles/cloud/config.json") || stateDir() != filepath.Join(root, "state/cairn/profiles/cloud") || unitName("snapshot") != "cairn-cloud-snapshot" {
		t.Fatal("named profile layout wrong")
	}
	if err := os.WriteFile(passwordFile(), []byte("cloud password"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prunedFile(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(progressFile("snapshot"), []byte(`{"percent_done":0.5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := (Restic{bin: "/bin/true"}).cmd("snapshots")
	if !slices.Contains(cmd.Args, passwordFile()) || slices.Contains(cmd.Args, filepath.Join(root, "config/cairn/password")) {
		t.Fatal("wrong profile password")
	}
	if got := undoCommand("/tmp/notes.txt", "saved"); !strings.HasPrefix(got, "cairn --profile cloud restore --at saved") {
		t.Fatal("undo loses profile: " + got)
	}
	if got := undoCommand("/tmp/notes\n.txt", "saved"); !strings.Contains(got, "profile cloud: snapshot saved") || strings.ContainsAny(got, "\n\r") {
		t.Fatal("unsafe-path undo loses profile or contains terminal controls: " + got)
	}
	selectedProfile = "default"
	got, _ := os.ReadFile(configFile())
	password, _ := os.ReadFile(passwordFile())
	if string(got) != string(legacyBytes) || string(password) != "legacy password" {
		t.Fatal("creating another profile modified legacy data")
	}
	if _, err := os.Stat(prunedFile()); !os.IsNotExist(err) {
		t.Fatal("pruning state leaked across profiles")
	}
	if _, err := os.Stat(progressFile("snapshot")); !os.IsNotExist(err) {
		t.Fatal("progress leaked across profiles")
	}
	if strings.Contains(undoCommand("/tmp/notes.txt", "saved"), "--profile") {
		t.Fatal("legacy undo changed")
	}
}

func TestProfilesRejectInvalidConfigurationsAndSymlinks(t *testing.T) {
	root := profileFixture(t)
	c := saveFixtureProfile(t, "default", filepath.Join(root, "legacy"))
	for _, contents := range []string{"broken JSON", "{}"} {
		if err := os.WriteFile(configFile(), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cmdInit(filepath.Join(root, "new"), false, nil, true, nil); err == nil {
			t.Fatal("init replaced an invalid config")
		}
		got, _ := os.ReadFile(configFile())
		if string(got) != contents {
			t.Fatal("invalid configuration was overwritten")
		}
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	invalid := c
	invalid.Repo = "relative-repository"
	if err := invalid.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("relative repository escaped profile exclusions")
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(configRoot(), "profiles")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(parent, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredProfiles(); err == nil {
		t.Fatal("inventory ignored linked profile")
	}
	selectedProfile = "linked"
	if err := c.save(); err == nil {
		t.Fatal("profile save wrote through symbolic link")
	}
	if _, _, err := setupPassword(); err == nil {
		t.Fatal("password setup wrote through symbolic link")
	}
}

func TestProfilesExcludeAndProtectEveryLocalRepository(t *testing.T) {
	root := profileFixture(t)
	repo := filepath.Join(root, "legacy")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	saveFixtureProfile(t, "default", repo)
	other := filepath.Join(root, "named[repo]")
	c := saveFixtureProfile(t, "nas", other)
	selectedProfile = "nas"
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	if err := checkProfileRepository(alias); err == nil {
		t.Fatal("same repository accepted through alias")
	}
	excludes, err := c.excludeArgs()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{repo, other, filepath.Join(stateRoot(), "rebuild"), filepath.Join(stateDir(), "rebuild")} {
		if !slices.Contains(excludes, globEscape(path)) {
			t.Errorf("missing exclusion for %s", path)
		}
	}
	r := scriptedRestic(t, "exit 99\n")
	for _, path := range []string{repo, filepath.Join(repo, "new-output"), filepath.Join(configRoot(), "password"), filepath.Join(configRoot(), "profiles/other/password")} {
		if _, err := saveBeforeRestore(r, c, path); err == nil || strings.Contains(err.Error(), "exit 99") {
			t.Errorf("protected path reached restic: %s %v", path, err)
		}
	}
}

func TestProfileSchedulesPreserveLegacyUnitsAndXDGRoots(t *testing.T) {
	root := profileFixture(t)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "systemctl.log")
	t.Setenv("CAIRN_TEST_SYSTEMCTL_LOG", log)
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CAIRN_TEST_SYSTEMCTL_LOG\"\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- executable isolated fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	c := defaultConfig()
	if err := applySchedule(c); err != nil {
		t.Fatal(err)
	}
	legacyService := filepath.Join(unitDir(), "cairn-snapshot.service")
	before, _ := os.ReadFile(legacyService) // #nosec G304 -- isolated fixture path
	selectedProfile = "nas"
	c.Backup.Every = "off"
	if err := applySchedule(c); err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(unitDir(), "cairn-nas-snapshot.service")
	b, err := os.ReadFile(service) // #nosec G304 -- isolated fixture path
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `--profile "nas" snapshot`) {
		t.Fatal("timer lost profile")
	}
	for _, kind := range []string{"CONFIG", "STATE", "DATA", "CACHE"} {
		if !strings.Contains(string(b), `Environment="XDG_`+kind+`_HOME=`+os.Getenv("XDG_"+kind+"_HOME")+`"`) {
			t.Errorf("timer lost %s root", kind)
		}
	}
	after, _ := os.ReadFile(legacyService) // #nosec G304 -- isolated fixture path
	if string(after) != string(before) {
		t.Fatal("named schedule changed legacy service")
	}
	if _, err := os.Stat(filepath.Join(unitDir(), "cairn-nas-backup.timer")); !os.IsNotExist(err) {
		t.Fatal("off schedule has timer")
	}
	if err := startJob("snapshot"); err != nil {
		t.Fatal(err)
	}
	readUnits("backup")
	commands, _ := os.ReadFile(log) // #nosec G304 -- isolated fixture path
	for _, want := range []string{"enable --now cairn-snapshot.timer", "enable --now cairn-nas-snapshot.timer", "disable --now cairn-nas-backup.timer", "start --no-block cairn-nas-snapshot.service", "cairn-nas-backup.timer cairn-nas-backup.service"} {
		if !strings.Contains(string(commands), want) {
			t.Errorf("systemctl lost profile: %s", want)
		}
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "unsafe%root"))
	if err := applySchedule(c); err == nil {
		t.Fatal("unsafe root scheduled")
	}
	unchanged, _ := os.ReadFile(service) // #nosec G304 -- isolated fixture path
	if string(unchanged) != string(b) {
		t.Fatal("unsafe scheduling partially changed units")
	}
}

func TestProfilesShareBackupLock(t *testing.T) {
	profileFixture(t)
	release, err := lockJobs(false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	selectedProfile = "nas"
	acquired := make(chan func(), 1)
	failed := make(chan error, 1)
	go func() {
		next, err := lockJobs(false)
		if err != nil {
			failed <- err
			return
		}
		acquired <- next
	}()
	select {
	case next := <-acquired:
		next()
		t.Fatal("named job bypassed default lock")
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case next := <-acquired:
		next()
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("named job did not acquire released lock")
	}
}

func TestFailedProfileInitRollsBackNewPassword(t *testing.T) {
	root := profileFixture(t)
	selectedProfile = "nas"
	c := saveFixtureProfile(t, "nas", filepath.Join(root, "existing"))
	before, _ := os.ReadFile(configFile())
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "restic"), []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf 'restic 0.19.1\\n'; exit; fi\nexit 1\n"), 0o700); err != nil { // #nosec G306 -- executable isolated fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := cmdInit(filepath.Join(root, "new"), true, nil, true, nil); err == nil {
		t.Fatal("failed repository access succeeded")
	}
	if _, err := os.Stat(passwordFile()); !os.IsNotExist(err) {
		t.Fatal("unused generated password remained")
	}
	after, _ := os.ReadFile(configFile())
	if string(before) != string(after) {
		t.Fatal("failed init changed old config")
	}
	if err := os.WriteFile(passwordFile(), []byte("keep existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdInit(c.Repo, true, nil, true, nil); err == nil {
		t.Fatal("failed connection succeeded")
	}
	password, _ := os.ReadFile(passwordFile())
	if string(password) != "keep existing" {
		t.Fatal("failed init removed existing password")
	}
}

func TestSavedProfileManifestPaths(t *testing.T) {
	for _, path := range []string{"/home/u/.local/state/cairn/manifests", "/tmp/state/cairn/profiles/nas/manifests", "/state/cairn/profiles/a-2/manifests"} {
		if !savedManifestPath(path) {
			t.Errorf("missing package manifests: %s", path)
		}
	}
	for _, path := range []string{"/home/u/cairn/work/manifests", "/state/cairn/profiles/default/manifests", "/state/cairn/profiles/../manifests", "/state/cairn/profiles/UPPER/manifests", "/state/cairn/profiles/nas/nested/manifests"} {
		if savedManifestPath(path) {
			t.Errorf("unrelated directory treated as manifests: %s", path)
		}
	}
}

func TestProfilesProtectExternalConfigurationAndPasswordLinks(t *testing.T) {
	root := profileFixture(t)
	saveFixtureProfile(t, "default", filepath.Join(root, "legacy"))
	c := saveFixtureProfile(t, "cloud", filepath.Join(root, "cloud"))
	secrets := filepath.Join(root, "secrets")
	if err := os.Mkdir(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(secrets, "password")
	if err := os.WriteFile(password, []byte("fixture password"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(password, passwordFile()); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(secrets, "config.json")
	cloudConfig := filepath.Join(profileDir(configRoot(), "cloud"), "config.json")
	if err := os.Rename(cloudConfig, config); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(config, cloudConfig); err != nil {
		t.Fatal(err)
	}
	// An invocation also protects other profiles, not just its own credential files.
	selectedProfile = "nas"
	r := scriptedRestic(t, "exit 99\n")
	for _, destination := range []string{password, config, secrets} {
		if _, err := saveBeforeRestore(r, c, destination); err == nil || strings.Contains(err.Error(), "exit 99") {
			t.Errorf("linked control reached restic: %s %v", destination, err)
		}
	}
	if err := os.Remove(password); err != nil {
		t.Fatal(err)
	}
	if _, err := saveBeforeRestore(r, c, password); err == nil {
		t.Fatal("dangling password target accepted")
	}
	copy := filepath.Join(root, "unrelated-copy")
	r = scriptedRestic(t, `while [ "$#" -gt 0 ]; do
  if [ "$1" = --target ]; then printf 'saved copy' > "$2"; exit; fi
  shift
done
exit 99
`)
	if err := publishCopy(r, Snap{ID: "saved"}, Node{Type: "file"}, "/old/notes.txt", copy, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(copy) // #nosec G304 -- isolated recovery fixture
	if string(b) != "saved copy" {
		t.Fatal("unrelated recovery was blocked")
	}
}

func TestNamedProfileSelectionHints(t *testing.T) {
	profileFixture(t)
	selectedProfile = "nas"
	r := scriptedRestic(t, "printf '[]\\n'\n")
	_, _, err := resolve(r, Config{Host: "fixture"}, "/notes.txt", "latest")
	if err == nil || !strings.Contains(err.Error(), "cairn --profile nas status") {
		t.Fatalf("status hint loses selection: %v", err)
	}
	r = scriptedRestic(t, `case " $* " in
  *" snapshots "*) printf '[{"id":"abc12345","time":"2026-01-01T00:00:00Z","hostname":"fixture","paths":["/"],"tags":["snapshot"]}]\n' ;;
  *) printf '[]\n' ;;
esac
`)
	_, _, err = resolve(r, Config{Host: "fixture"}, "/notes.txt", "latest")
	if err == nil || !strings.Contains(err.Error(), "cairn --profile nas history") {
		t.Fatalf("history hint loses selection: %v", err)
	}
}

func TestDataOnlyExcludesAllProfileRecipes(t *testing.T) {
	root := profileFixture(t)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "home/.local/state[old]"))
	saveFixtureProfile(t, "default", filepath.Join(root, "legacy"))
	saveFixtureProfile(t, "nas", filepath.Join(root, "nas"))
	c := saveFixtureProfile(t, "cloud", filepath.Join(root, "cloud"))
	selectedProfile = "cloud"
	c.BackupScope = scopeDataOnly
	args, err := c.excludeArgs()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "nas", "cloud"} {
		manifest := globEscape(filepath.Join(profileDir(stateRoot(), name), "manifests"))
		if !slices.Contains(args, manifest) {
			t.Errorf("included %s machine recipe", name)
		}
	}
	if !slices.Contains(args, globEscape(filepath.Join(stateRoot(), "profiles"))+"/*/manifests") {
		t.Fatal("unconfigured stale profile recipes can enter HOME backups")
	}
	shared := dataDir()
	selectedProfile = "default"
	if dataDir() != shared {
		t.Fatal("dependency binaries became profile-specific")
	}
	selectedProfile = "cloud"
	c.BackupScope = scopeMachine
	args, err = c.excludeArgs()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, globEscape(filepath.Join(profileDir(stateRoot(), "nas"), "manifests"))) {
		t.Fatal("machine profile cannot retain saved recipes")
	}
}

func TestNamedMissingRemoteGuidance(t *testing.T) {
	profileFixture(t)
	dir := cloudFixture(t)
	selectedProfile = "cloud"
	cloudTool(t, dir, "rclone", "printf 'different:\\n'\n")
	_, err := repoFor("missing:backups")
	if err == nil || !strings.Contains(err.Error(), "cairn --profile cloud cloud setup") || !strings.Contains(err.Error(), "cairn --profile cloud init") {
		t.Fatalf("missing remote hint loses profile: %v", err)
	}
}

func TestNamedDataOnlyRebuildGuidance(t *testing.T) {
	profileFixture(t)
	selectedProfile = "cloud"
	err := cmdRebuild(Config{BackupScope: scopeDataOnly}, "", "", false)
	for _, command := range []string{"history", "recover", "restore"} {
		if err == nil || !strings.Contains(err.Error(), "cairn --profile cloud "+command) {
			t.Fatalf("rebuild hint loses profile: %v", err)
		}
	}
}
