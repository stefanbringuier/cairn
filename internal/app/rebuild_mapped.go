package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func cmdRebuildMapped(c Config, options rebuildOptions) error {
	if options.From != "" && (options.NoHome || c.backupScope() == scopeDataOnly) {
		return fmt.Errorf("--from cannot be combined with --no-home or data-only scope")
	}
	if len(options.Maps) == 0 && (c.backupScope() == scopeDataOnly || options.NoHome) {
		return fmt.Errorf("a data-only or --no-home rebuild requires at least one --map")
	}
	if !options.Yes && !isTTY() {
		return needsYes("Rebuild")
	}
	mappings, err := parseRebuildMappings(options.Maps)
	if err != nil {
		return err
	}
	if err := validateRebuildMappings(c, mappings); err != nil {
		return err
	}
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	plan, err := planRebuild(c, r, options, mappings)
	if err != nil {
		return err
	}
	printRebuildPlan(plan)
	if !options.Yes {
		if err := confirm("Rebuild from "+clean(plan.Host)+"?", "Restore the listed paths. Existing mapped destinations are saved first; stop other writers before continuing. Configuration changes only after every mapped restore succeeds.", "Rebuild"); err != nil {
			return err
		}
	}
	// Mappings precede the broad home restore. A failed mapping cannot overwrite
	// the saved config/password by restoring a whole home first.
	for i, item := range plan.Mappings {
		undo, err := saveBeforeRestore(r, c, item.To)
		if err != nil {
			return fmt.Errorf("mapping %d failed; %d completed; configuration unchanged: %w", i+1, i, err)
		}
		if undo != "" {
			note("undo: " + undoCommand(item.To, undo))
		}
		if err := restoreSaved(r, item.Snapshot, item.Node, item.From, item.To, undo); err != nil {
			return fmt.Errorf("mapping %d failed; %d completed; configuration unchanged (keep the printed undo commands): %w", i+1, i, err)
		}
		ok("restored " + tildify(item.From) + " → " + tildify(item.To))
	}
	updated := c
	rewrites := plan.Mappings
	if plan.Home != nil {
		item := *plan.Home
		note("restoring whole home " + tildify(item.From) + " → " + tildify(home))
		if _, err := r.out("restore", "--target", home, "--", item.Snapshot.ID+":"+item.From); err != nil {
			return fmt.Errorf("mapped restores completed; home restore failed and may be partial; inspect restored configuration before retrying: %w", err)
		}
		if old, err := loadConfig(); err == nil && old.Repo == c.Repo {
			old.Target, old.Since = c.Target, c.Since
			updated = old
		}
		rewrites = append(rewrites, item)
	}
	updated = remapRebuildConfig(updated, rewrites)
	updated.Host = plan.Host
	if err := updated.validate(); err != nil {
		return fmt.Errorf("restores completed but configuration not saved: %w", err)
	}
	if err := updated.save(); err != nil {
		return fmt.Errorf("restores completed but configuration not saved: %w", err)
	}
	for _, kind := range []string{"snapshot", "backup"} {
		before, after := c.job(kind).Paths, updated.job(kind).Paths
		if strings.Join(before, "\x00") != strings.Join(after, "\x00") {
			for _, path := range after {
				note(kind + " path: " + tildify(path))
			}
		}
	}
	for _, path := range plan.SavedPaths {
		covered := false
		for _, item := range rewrites {
			covered = covered || withinPath(path, item.From)
		}
		if covered || savedManifestPath(path) || path == "/etc" || path == "/usr/share/keyrings" {
			continue
		}
		if clean(path) != path || clean(plan.Host) != plan.Host {
			note("also in this backup: " + clean(path) + " (use --map with the original exact path)")
			continue
		}
		quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
		note("also in this backup: " + clean(path) + " → `" + profileCommand() + " rebuild --host " + quote(plan.Host) + " --no-home --map " + quote(path+"="+path) + "` (review destination before running)")
	}
	note("new runs will use host " + clean(plan.Host) + "; re-run init to start a fresh host series")
	if err := applySchedule(updated); err != nil {
		warn(err.Error())
	}
	if plan.Packages {
		from := ""
		if plan.Home != nil {
			from = plan.Home.From
		}
		recipe := plan.Recipe
		recipe.Paths = slices.DeleteFunc(slices.Clone(recipe.Paths), func(path string) bool {
			return path != "/etc" && path != "/usr/share/keyrings" && !savedManifestPath(path)
		})
		root := filepath.Join(stateDir(), "rebuild")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		stage, err := os.MkdirTemp(root, "run-")
		if err != nil {
			return err
		}
		return rebuildPackagesAt(r, recipe, from, options.Yes, stage)
	}
	ok("rebuild done; package and /etc steps skipped")
	return nil
}
