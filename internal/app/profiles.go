package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Selection lasts for one invocation; default preserves the original file layout.
var selectedProfile = "default"

func validProfile(name string) bool {
	if len(name) < 1 || len(name) > 48 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		letter, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
		if !letter && !digit && r != '-' {
			return false
		}
	}
	return true
}

func selectProfile(args []string) (string, []string, error) {
	name := "default"
	if len(args) == 0 {
		return name, args, nil
	}
	if args[0] == "--profile" {
		if len(args) < 2 {
			return "", nil, &cliError{exitNeedsYes, "--profile needs a name"}
		}
		name, args = args[1], args[2:]
	} else if value, found := strings.CutPrefix(args[0], "--profile="); found {
		name, args = value, args[1:]
	}
	if !validProfile(name) {
		return "", nil, &cliError{exitNeedsYes, "profile names need 1–48 lowercase letters, digits or hyphens, starting and ending with a letter or digit"}
	}
	return name, args, nil
}

func profileDir(root, name string) string {
	if name == "default" {
		return root
	}
	return filepath.Join(root, "profiles", name)
}

func profileCommand() string {
	if selectedProfile == "default" {
		return "cairn"
	}
	return "cairn --profile " + selectedProfile
}

// Profile directories cannot redirect credentials or writes outside the profile root.
func checkProfileDirectory(name string) error {
	if name == "default" {
		return nil
	}
	for _, path := range []string{filepath.Join(configRoot(), "profiles"), profileDir(configRoot(), name)} {
		if err := checkProfilePath(path); err != nil {
			return err
		}
	}
	return nil
}

func checkProfilePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("profile directories must be real directories: %s", clean(path))
	}
	return nil
}

func unitName(kind string) string {
	if selectedProfile == "default" {
		return "cairn-" + kind
	}
	return "cairn-" + selectedProfile + "-" + kind
}

func configuredProfiles() (map[string]Config, error) {
	names := []string{"default"}
	if err := checkProfilePath(filepath.Join(configRoot(), "profiles")); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(configRoot(), "profiles"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 && validProfile(entry.Name()) {
			return nil, fmt.Errorf("profile directory %s must not be a symbolic link", entry.Name())
		}
		if entry.IsDir() && entry.Name() != "default" && validProfile(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	profiles := make(map[string]Config)
	for _, name := range names {
		c, err := loadProfile(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		profiles[name] = c
	}
	return profiles, nil
}

// All configured repositories are excluded and protected, including symlink aliases.
func localRepositories(c Config, profiles map[string]Config) []string {
	repos := []string{}
	add := func(config Config) {
		if filepath.IsAbs(config.Repo) {
			for _, repo := range []string{config.Repo, repositoryIdentity(config.Repo)} {
				if !slices.Contains(repos, repo) {
					repos = append(repos, repo)
				}
			}
		}
	}
	add(c)
	for _, config := range profiles {
		add(config)
	}
	return repos
}

func profileControls(profiles map[string]Config) []string {
	names := []string{"default", selectedProfile}
	for name := range profiles {
		names = append(names, name)
	}
	controls := []string{configRoot()}
	for _, name := range names {
		dir := profileDir(configRoot(), name)
		for _, file := range []string{"config.json", "password"} {
			path := filepath.Join(dir, file)
			if !slices.Contains(controls, path) {
				controls = append(controls, path)
			}
		}
	}
	return controls
}

// Credential files may point outside their profile directory, even to an absent file.
func canonicalControl(path string) (string, error) {
	for range 40 {
		target, err := os.Readlink(path)
		if err != nil {
			return canonicalPath(path)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", fmt.Errorf("configuration/password symbolic links cannot be resolved")
}

func repositoryIdentity(repo string) string {
	if filepath.IsAbs(repo) {
		if resolved, err := canonicalPath(repo); err == nil {
			return resolved
		}
		return filepath.Clean(repo)
	}
	return repo
}

func checkProfileRepository(repo string) error {
	profiles, err := configuredProfiles()
	if err != nil {
		return err
	}
	for name, c := range profiles {
		if name != selectedProfile && repositoryIdentity(c.Repo) == repositoryIdentity(repo) {
			return fmt.Errorf("repository is already used by profile %s; choose a separate repository so retention cannot remove another profile's snapshots", name)
		}
	}
	return nil
}

type profileTimer struct {
	Every   string `json:"every"`
	Timer   string `json:"timer"`
	Enabled *bool  `json:"enabled"`
}

type profileInfo struct {
	Name        string       `json:"name"`
	Target      string       `json:"target"`
	BackupScope string       `json:"backup_scope"`
	Snapshot    profileTimer `json:"snapshot"`
	Backup      profileTimer `json:"backup"`
}

func timerEnabled(timer string) *bool {
	out, _ := systemctl("is-enabled", timer)
	var enabled bool
	switch strings.TrimSpace(string(out)) {
	case "enabled", "enabled-runtime":
		enabled = true
	case "disabled", "masked", "masked-runtime", "static", "indirect", "not-found":
	default:
		return nil
	}
	return &enabled
}

func cmdProfiles(asJSON bool) error {
	profiles, err := configuredProfiles()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	infos := make([]profileInfo, 0, len(names))
	for _, name := range names {
		c := profiles[name]
		unit := "cairn-"
		if name != "default" {
			unit += name + "-"
		}
		snapshot, backup := unit+"snapshot.timer", unit+"backup.timer"
		infos = append(infos, profileInfo{
			Name: name, Target: redact(c.Target), BackupScope: c.backupScope(),
			Snapshot: profileTimer{c.Snapshot.Every, snapshot, timerEnabled(snapshot)},
			Backup:   profileTimer{c.Backup.Every, backup, timerEnabled(backup)},
		})
	}
	if asJSON {
		b, _ := json.MarshalIndent(infos, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(infos) == 0 {
		fmt.Println("No profiles yet: run cairn init <target>")
	}
	for _, info := range infos {
		fmt.Printf("%s  %s  %s\n", info.Name, info.BackupScope, clean(info.Target))
		for _, entry := range []profileTimer{info.Snapshot, info.Backup} {
			state := "unknown (systemd unavailable)"
			if entry.Enabled != nil {
				state = "disabled"
				if *entry.Enabled {
					state = "enabled"
				}
			}
			fmt.Printf("  %s  every %s  %s\n", entry.Timer, clean(entry.Every), state)
		}
	}
	return nil
}
