package app

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
)

// pick resolves --at: a snapshot id (prefix) wins, else a time cutoff ("latest", a date, or an age).
func pick(snaps []Snap, at string, now time.Time) (cutoff time.Time, id string, err error) {
	if at == "" || at == "latest" {
		return now, "", nil
	}
	if len(at) >= 4 && strings.Trim(at, "0123456789abcdef") == "" {
		var hits []string
		for _, s := range snaps {
			if strings.HasPrefix(s.ID, at) {
				hits = append(hits, s.ID)
			}
		}
		if len(hits) > 1 {
			return time.Time{}, "", fmt.Errorf("snapshot id %q matches %d snapshots: use more characters", at, len(hits))
		}
		if len(hits) == 1 {
			return time.Time{}, hits[0], nil
		}
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, at, time.Local); err == nil {
			if layout == "2006-01-02" {
				t = t.Add(24*time.Hour - time.Second) // "as of that day" = end of day
			}
			return t, "", nil
		}
	}
	units := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	if n, err := strconv.Atoi(at[:max(len(at)-1, 0)]); err == nil && n >= 0 && n <= 100_000 {
		if u, ok := units[at[len(at)-1]]; ok {
			return now.Add(-time.Duration(n) * u), "", nil
		}
	}
	return time.Time{}, "", fmt.Errorf("bad --at %q: use latest, a snapshot id, 2026-10-01, \"2026-10-01 14:00\" or an age like 30m, 6h, 3d, 2w", at)
}

// candidates are snapshots that could hold p as of the cutoff, newest first (safety snapshots only by id).
func candidates(snaps []Snap, p string, cutoff time.Time, id string) []Snap {
	var out []Snap
	for _, s := range snaps {
		if id != "" {
			if s.ID == id {
				return []Snap{s}
			}
			continue
		}
		if s.kind() != "pre-restore" && !s.Time.After(cutoff) && s.covers(p) {
			out = append(out, s)
		}
	}
	slices.Reverse(out)
	return out
}

// findExact maps snapshot id -> node for every snapshot holding exactly path p (one index load).
func findExact(r Restic, c Config, p string) (map[string]Node, error) {
	b, err := r.out("find", "--json", "--no-lock", "--host", c.Host, "--", globEscape(p))
	if err != nil {
		return nil, err
	}
	var found []struct {
		Snapshot string `json:"snapshot"`
		Matches  []Node `json:"matches"`
	}
	if err := json.Unmarshal(b, &found); err != nil {
		return nil, err
	}
	out := map[string]Node{}
	for _, f := range found {
		for _, m := range f.Matches {
			if m.Path == p {
				m.Name = filepath.Base(p)
				out[f.Snapshot] = m
			}
		}
	}
	return out, nil
}

// resolve finds the snapshot to restore p from, and p's node in it.
func resolve(r Restic, c Config, p, at string) (Snap, Node, error) {
	snaps, err := r.snapshots(c.Host)
	if err != nil {
		return Snap{}, Node{}, err
	}
	cutoff, id, err := pick(snaps, at, time.Now())
	if err != nil {
		return Snap{}, Node{}, err
	}
	cands := candidates(snaps, p, cutoff, id)
	if len(cands) == 0 {
		return Snap{}, Node{}, fmt.Errorf("no snapshot up to %s covers %s (cairn status shows what's backed up)", at, tildify(p))
	}
	if nodes, err := r.ls(cands[0].ID, p); err != nil {
		return Snap{}, Node{}, err
	} else if len(nodes) > 0 && nodes[0].Path == p {
		return cands[0], nodes[0], nil
	}
	if id == "" { // gone from the newest snapshot: look further back in one pass
		found, err := findExact(r, c, p)
		if err != nil {
			return Snap{}, Node{}, err
		}
		for _, s := range cands[1:] {
			if n, ok := found[s.ID]; ok {
				return s, n, nil
			}
		}
	}
	return Snap{}, Node{}, fmt.Errorf("%s isn't in the snapshots up to %s (try `cairn history %s`)", tildify(p), at, tildify(p))
}

// includeArgs restores just p (file or dir) out of s into target; extra flags go before "--".
func includeArgs(s Snap, p, target string, extra ...string) []string {
	return slices.Concat([]string{"restore", "--target", target, "--include", "/" + globEscape(filepath.Base(p))},
		extra, []string{"--", s.ID + ":" + filepath.Dir(p)})
}

type preview struct {
	Updated, Restored, Unchanged int
	Examples                     []string
}

func (pv preview) lines() []string {
	if pv.Updated == 0 && pv.Restored == 0 {
		return []string{good.Render("Already identical: nothing would change.")}
	}
	out := []string{fmt.Sprintf("overwrite %d changed · recreate %d missing · %d unchanged", pv.Updated, pv.Restored, pv.Unchanged)}
	for _, e := range pv.Examples {
		out = append(out, dim.Render("  "+e))
	}
	return append(out, dim.Render("Your current version is snapshotted first (tag pre-restore). Newer files not in the snapshot are kept."))
}

func (r Restic) dryRun(s Snap, p string) (preview, error) {
	var pv preview
	dir := filepath.Dir(p)
	b, err := r.out(includeArgs(s, p, dir, "--dry-run", "--json", "--verbose=2")...)
	if err != nil {
		return pv, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m struct {
			Type   string `json:"message_type"`
			Action string `json:"action"`
			Item   string `json:"item"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type != "verbose_status" {
			continue
		}
		local := filepath.Join(dir, m.Item)
		switch m.Action {
		case "updated":
			pv.Updated++
			if len(pv.Examples) < 5 {
				pv.Examples = append(pv.Examples, tildify(local))
			}
		case "restored": // existing dirs show up here too (metadata only); count only what's actually missing
			if _, err := os.Lstat(local); err != nil {
				pv.Restored++
			}
		case "unchanged":
			pv.Unchanged++
		}
	}
	return pv, nil
}

// restoreInPlace snapshots the current version (tag pre-restore), then puts s's version of p back.
// It returns the safety snapshot's id ("" if p didn't exist) so the restore can be undone.
func restoreInPlace(r Restic, c Config, s Snap, n Node, p string) (string, error) {
	undo, err := saveBeforeRestore(r, c, p)
	if err != nil {
		return "", err
	}
	err = restoreSaved(r, s, n, p, p, undo)
	if err != nil && undo != "" {
		err = fmt.Errorf("%w; undo: %s", err, undoCommand(p, undo))
	}
	return undo, err
}

func undoCommand(path, id string) string {
	if clean(path) != path {
		return "snapshot " + id + " (restore using the original path)"
	}
	return "cairn restore --at " + id + " -y -- '" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

// saveBeforeRestore must protect all current content, even normally excluded files.
func saveBeforeRestore(r Restic, c Config, path string) (string, error) {
	info, err := os.Lstat(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return "", err
	}
	if !missing && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing to replace a symbolic link: use cairn recover with a new destination")
	}
	root, err := canonicalPath(path)
	if err != nil {
		return "", err
	}
	for _, control := range []string{configFile(), passwordFile()} {
		if resolved, err := canonicalPath(control); err == nil {
			control = resolved
		}
		if withinPath(control, root) {
			return "", fmt.Errorf("destination contains active cairn configuration or password; use cairn recover to keep undo access available")
		}
	}
	if filepath.IsAbs(c.Repo) {
		repo, repoErr := canonicalPath(c.Repo)
		if repoErr == nil && (withinPath(root, repo) || withinPath(repo, root)) {
			return "", fmt.Errorf("restore destination overlaps the backup repository; use cairn recover with a new destination")
		}
	}
	if missing {
		return "", nil
	}
	b, err := r.out("backup", "--json", "--host", c.Host, "--tag", "pre-restore", "--retry-lock", "10m", "--", path)
	if err != nil {
		return "", fmt.Errorf("safety snapshot incomplete or failed, nothing restored: %w", err)
	}
	var sum struct {
		Type string `json:"message_type"`
		summary
	}
	if err := json.Unmarshal([]byte(lastLine(string(b))), &sum); err != nil {
		return "", fmt.Errorf("cannot verify safety snapshot, nothing restored: %w", err)
	}
	if id, err := hex.DecodeString(sum.SnapshotID); err != nil || len(id) != 32 || sum.Type != "summary" {
		return "", fmt.Errorf("missing safety snapshot ID, nothing restored")
	}
	nodes, err := r.ls(sum.SnapshotID, path)
	if err != nil {
		return "", fmt.Errorf("cannot verify safety snapshot, nothing restored: %w", err)
	}
	if len(nodes) == 0 || nodes[0].Path != path {
		return "", fmt.Errorf("current path missing from safety snapshot, nothing restored")
	}
	return sum.SnapshotID, nil
}

// canonicalPath resolves existing ancestors as well as paths that have not been created yet.
func canonicalPath(path string) (string, error) {
	missing := ""
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return filepath.Join(resolved, missing), nil
		}
		parent := filepath.Dir(path)
		if !os.IsNotExist(err) || parent == path {
			return "", err
		}
		missing = filepath.Join(filepath.Base(path), missing)
		path = parent
	}
}

// restoreSaved writes only the exact destination whose current contents were saved.
func restoreSaved(r Restic, s Snap, n Node, p, to, undo string) error {
	if undo == "" {
		return publishCopy(r, s, n, p, to, true)
	}
	if filepath.Base(p) == filepath.Base(to) {
		_, err := r.out(includeArgs(s, p, filepath.Dir(to))...)
		return err
	}
	if n.Type == "dir" {
		_, err := r.out("restore", "--target", to, "--", s.ID+":"+p)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	_, err := r.out("dump", "--target", to, "--", s.ID, p)
	return err
}

func cmdRestore(c Config, p, at, to string, yes bool) error {
	p = expand(p)
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	s, n, err := resolve(r, c, p, at)
	if err != nil {
		return err
	}
	from := fmt.Sprintf("%s %s (%s)", s.kind(), s.ShortID, ago(s.Time))
	if to != "" {
		to = expand(to)
		dest := to
		if fi, err := os.Stat(to); err == nil && fi.IsDir() {
			dest = filepath.Join(to, filepath.Base(p))
		}
		if dest != p { // landing on p itself is just an in-place restore (with its safety snapshot)
			if _, err := os.Lstat(dest); err == nil && !yes {
				if err := confirm("Overwrite "+tildify(dest)+"?", "It will be replaced by "+tildify(p)+" from "+from+".", "Overwrite"); err != nil {
					return err
				}
			}
			undo, err := saveBeforeRestore(r, c, dest)
			if err != nil {
				return err
			}
			if undo != "" {
				note("undo: " + undoCommand(dest, undo))
			}
			if err := restoreSaved(r, s, n, p, dest, undo); err != nil {
				return err
			}
			ok("restored " + tildify(p) + " from " + from + " → " + tildify(dest))
			return nil
		}
	}
	if !yes {
		pv, err := r.dryRun(s, p)
		if err != nil {
			return err
		}
		if pv.Updated == 0 && pv.Restored == 0 {
			ok(tildify(p) + " already matches " + from)
			return nil
		}
		if err := confirm("Restore "+tildify(p)+" from "+from+"?", strings.Join(pv.lines(), "\n"), "Restore"); err != nil {
			return err
		}
	}
	undo, err := saveBeforeRestore(r, c, p)
	if err != nil {
		return err
	}
	if undo != "" {
		note("undo: " + undoCommand(p, undo))
	}
	if err := restoreSaved(r, s, n, p, p, undo); err != nil {
		return err
	}
	ok("restored " + tildify(p) + " from " + from)
	return nil
}

type Version struct {
	Snapshot string    `json:"snapshot"`
	Kind     string    `json:"kind"`
	Time     time.Time `json:"time"`
	Size     int64     `json:"size,omitempty"`
	Mtime    time.Time `json:"mtime,omitzero"`
}

// history lists versions of p, newest first: one `find` for files; covering snapshots for dirs
// (find on a dir returns every file under it).
func history(r Restic, c Config, p string) ([]Version, error) {
	snaps, err := r.snapshots(c.Host)
	if err != nil {
		return nil, err
	}
	isDir := false
	if fi, err := os.Stat(p); err == nil {
		isDir = fi.IsDir()
	} else if s := candidates(snaps, p, time.Now(), ""); len(s) > 0 {
		if nodes, err := r.ls(s[0].ID, p); err == nil && len(nodes) > 0 {
			isDir = nodes[0].Type == "dir"
		}
	}
	out := []Version{}
	var found map[string]Node
	if !isDir {
		if found, err = findExact(r, c, p); err != nil {
			return nil, err
		}
	}
	for _, s := range slices.Backward(snaps) {
		if n, ok := found[s.ID]; ok {
			out = append(out, Version{Snapshot: s.ID, Kind: s.kind(), Time: s.Time, Size: n.Size, Mtime: n.Mtime})
		} else if isDir && s.covers(p) {
			out = append(out, Version{Snapshot: s.ID, Kind: s.kind(), Time: s.Time})
		}
	}
	return out, nil
}

// clean drops control characters so names from a repo can't inject terminal escapes.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

func tildify(p string) string {
	if p == home || strings.HasPrefix(p, home+"/") {
		p = "~" + p[len(home):]
	}
	return clean(p)
}

func ago(t time.Time) string { return humanize.Time(t) }
