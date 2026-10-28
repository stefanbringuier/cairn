package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type rebuildOptions struct {
	Host, From, At          string
	Maps                    []string
	NoHome, NoPackages, Yes bool
}

type rebuildMapping struct {
	From, To string
	Snapshot Snap
	Node     Node
	Missing  bool
}

type rebuildPlan struct {
	SavedPaths []string
	Host       string
	Mappings   []rebuildMapping
	Home       *rebuildMapping
	Recipe     Snap
	Packages   bool
}

// parseRebuildMappings does not expand OLD against the current filesystem: it is
// an absolute name stored in a snapshot, not a current local source.
func parseRebuildMappings(values []string) ([]rebuildMapping, error) {
	var result []rebuildMapping
	seen := map[string]bool{}
	for _, value := range values {
		from, to, ok := strings.Cut(value, "=")
		if !ok || !filepath.IsAbs(from) || !filepath.IsAbs(to) {
			return nil, fmt.Errorf("--map requires absolute OLD=NEW paths")
		}
		from, to = filepath.Clean(from), filepath.Clean(to)
		if seen[from] {
			return nil, fmt.Errorf("duplicate --map source %s", tildify(from))
		}
		seen[from] = true
		result = append(result, rebuildMapping{From: from, To: to})
	}
	return result, nil
}

func validateRebuildMappings(c Config, mappings []rebuildMapping) error {
	canonical := make([]string, len(mappings))
	currentHome, err := canonicalPath(home)
	if err != nil {
		return err
	}
	for i := range mappings {
		missing, err := validateRestoreDestination(c, mappings[i].To)
		if err != nil {
			return fmt.Errorf("mapping %s: %w", tildify(mappings[i].To), err)
		}
		mappings[i].Missing = missing
		canonical[i], err = canonicalPath(mappings[i].To)
		if err != nil {
			return err
		}
		if withinPath(canonical[i], currentHome) || withinPath(currentHome, canonical[i]) {
			return fmt.Errorf("mapping destination overlaps the current home; use a separate destination")
		}
	}
	for i, item := range mappings {
		for j, other := range mappings {
			if i == j {
				continue
			}
			if withinPath(canonical[i], canonical[j]) || withinPath(canonical[j], canonical[i]) {
				return fmt.Errorf("mapping destinations overlap: %s and %s", tildify(item.To), tildify(other.To))
			}
			if withinPath(item.To, other.From) || withinPath(canonical[i], other.From) {
				return fmt.Errorf("mapping destination %s is inside another mapping's source %s", tildify(item.To), tildify(other.From))
			}
		}
	}
	return nil
}

func rebuildSources(c Config, all []Snap) []Snap {
	result := slices.DeleteFunc(slices.Clone(all), func(s Snap) bool {
		return slices.Contains(s.Tags, "pre-restore") || (s.kind() != "snapshot" && s.kind() != "backup") ||
			(!c.Since.IsZero() && !s.Time.Before(c.Since)) ||
			(c.backupScope() != scopeDataOnly && slices.Contains(s.Tags, scopeDataOnly))
	})
	slices.SortStableFunc(result, func(a, b Snap) int { return a.Time.Compare(b.Time) })
	return result
}

func resolveRebuildSource(r Restic, all []Snap, host, path, at string) (Snap, Node, error) {
	snaps := slices.DeleteFunc(slices.Clone(all), func(s Snap) bool { return s.Hostname != host })
	cutoff, id, err := pick(snaps, at, time.Now())
	if err != nil {
		return Snap{}, Node{}, err
	}
	for _, s := range slices.Backward(snaps) {
		if !s.covers(path) || (id != "" && s.ID != id) || (id == "" && s.Time.After(cutoff)) {
			continue
		}
		nodes, err := r.ls(s.ID, path)
		if err != nil {
			return Snap{}, Node{}, err
		}
		if len(nodes) > 0 && nodes[0].Path == path {
			return s, nodes[0], nil
		}
	}
	var paths []string
	for _, s := range snaps {
		paths = append(paths, s.Paths...)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	for i := range paths {
		paths[i] = clean(paths[i])
	}
	return Snap{}, Node{}, fmt.Errorf("no eligible snapshot from %s up to %s contains %s; saved paths: %s", clean(host), clean(at), tildify(path), strings.Join(paths, ", "))
}

func planRebuild(c Config, r Restic, options rebuildOptions, mappings []rebuildMapping) (rebuildPlan, error) {
	var plan rebuildPlan
	all, err := r.snapshots("")
	if err != nil {
		return plan, err
	}
	all = rebuildSources(c, all)
	host := options.Host
	if host == "" {
		hosts := map[string]bool{}
		for _, s := range all {
			hosts[s.Hostname] = true
		}
		if hosts[c.Host] {
			host = c.Host
		} else if len(hosts) == 1 {
			for h := range hosts {
				host = h
			}
		} else {
			return plan, fmt.Errorf("choose a source machine with --host (%d eligible hosts)", len(hosts))
		}
	}
	plan.Host = host
	for _, s := range all {
		if s.Hostname == host {
			plan.SavedPaths = append(plan.SavedPaths, s.Paths...)
		}
	}
	slices.Sort(plan.SavedPaths)
	plan.SavedPaths = slices.Compact(plan.SavedPaths)
	for _, s := range all {
		if s.Hostname == host && s.kind() == "backup" && !slices.Contains(s.Tags, scopeDataOnly) {
			plan.Recipe = s
		}
	}
	noHome := options.NoHome || c.backupScope() == scopeDataOnly
	if !noHome {
		from := options.From
		if from == "" {
			for _, p := range plan.Recipe.Paths {
				if filepath.Base(p) == plan.Recipe.Username {
					from = p
				}
			}
		}
		if from == "" {
			return plan, fmt.Errorf("cannot identify the old home; choose --from or --no-home")
		}
		if !filepath.IsAbs(from) {
			return plan, fmt.Errorf("--from must be an absolute saved path")
		}
		from = filepath.Clean(from)
		s, n, err := resolveRebuildSource(r, all, host, from, "latest")
		if err != nil {
			return plan, err
		}
		plan.Home = &rebuildMapping{From: from, To: home, Snapshot: s, Node: n}
		for _, mapping := range mappings {
			if withinPath(mapping.From, from) || withinPath(from, mapping.From) {
				return plan, fmt.Errorf("mapping source overlaps the old home; use --no-home for these paths")
			}
			canonical, err := canonicalPath(mapping.To)
			if err != nil {
				return plan, err
			}
			if withinPath(mapping.To, from) || withinPath(canonical, from) {
				return plan, fmt.Errorf("mapping destination is inside the old home")
			}
		}
	}
	plan.Packages = !options.NoPackages && c.backupScope() != scopeDataOnly &&
		slices.ContainsFunc(plan.Recipe.Paths, savedManifestPath)
	plan.Mappings = slices.Clone(mappings)
	for i := range plan.Mappings {
		item := &plan.Mappings[i]
		item.Snapshot, item.Node, err = resolveRebuildSource(r, all, host, item.From, options.At)
		if err != nil {
			return rebuildPlan{}, err
		}
	}
	return plan, nil
}

// Apply only the longest matching original prefix once. Nested mappings cannot
// rewrite each other's output, and source Config slices remain untouched.
func remapRebuildConfig(c Config, mappings []rebuildMapping) Config {
	c.Snapshot.Paths, c.Backup.Paths = slices.Clone(c.Snapshot.Paths), slices.Clone(c.Backup.Paths)
	for _, job := range []*Job{&c.Snapshot, &c.Backup} {
		for i, path := range job.Paths {
			best := -1
			for j, item := range mappings {
				if withinPath(path, item.From) && (best == -1 || len(item.From) > len(mappings[best].From)) {
					best = j
				}
			}
			if best >= 0 {
				item := mappings[best]
				suffix, _ := filepath.Rel(item.From, path)
				job.Paths[i] = filepath.Join(item.To, suffix)
			}
		}
	}
	return c
}

func printRebuildPlan(plan rebuildPlan) {
	note("source host: " + clean(plan.Host))
	for _, item := range plan.Mappings {
		exists := "missing destination"
		if !item.Missing {
			exists = "existing destination; safety snapshot before replacement"
		}
		note(fmt.Sprintf("%s → %s · %s %s (%s) · %s", tildify(item.From), tildify(item.To), item.Snapshot.kind(), item.Snapshot.ShortID, ago(item.Snapshot.Time), exists))
	}
	if plan.Home != nil {
		item := plan.Home
		note(fmt.Sprintf("home: %s → %s · %s %s (%s) · whole-home overwrite", tildify(item.From), tildify(home), item.Snapshot.kind(), item.Snapshot.ShortID, ago(item.Snapshot.Time)))
	}
	if plan.Packages {
		note("package steps: stage /etc and the chosen backup recipe, then offer each installation step")
	}
}
