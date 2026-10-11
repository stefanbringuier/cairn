package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

type DiffChange struct {
	Path     string `json:"path"`
	Modifier string `json:"modifier"`
}

type DiffSummary struct {
	Added           int `json:"added"`
	Removed         int `json:"removed"`
	Modified        int `json:"modified"`
	TypeChanged     int `json:"type_changed"`
	MetadataChanged int `json:"metadata_changed"`
	Damaged         int `json:"damaged"`
}

type SnapshotDiff struct {
	From    string       `json:"from"`
	To      string       `json:"to"`
	Path    string       `json:"path,omitempty"`
	Changes []DiffChange `json:"changes"`
	Summary DiffSummary  `json:"summary"`
}

func withinPath(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

func snapshotAt(snaps []Snap, at string) (Snap, error) {
	cutoff, id, err := pick(snaps, at, time.Now())
	if err != nil {
		return Snap{}, err
	}
	var selected Snap
	for _, s := range snaps {
		if id != "" && s.ID == id {
			return s, nil
		}
		if id == "" && s.kind() != "pre-restore" && !s.Time.After(cutoff) && (selected.ID == "" || s.Time.After(selected.Time)) {
			selected = s
		}
	}
	if selected.ID == "" {
		return Snap{}, fmt.Errorf("no snapshot up to %s for this host", at)
	}
	return selected, nil
}

func decodeDiff(data []byte, from, to, path string) (SnapshotDiff, error) {
	diff := SnapshotDiff{From: from, To: to, Path: path, Changes: []DiffChange{}}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(nil, 16<<20)
	complete := false
	for scanner.Scan() {
		var message struct {
			Type string `json:"message_type"`
			DiffChange
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return diff, fmt.Errorf("read snapshot diff: %w", err)
		}
		if message.Type == "statistics" {
			complete = true
		}
		if message.Type != "change" || (path != "" && !withinPath(message.Path, path)) {
			continue
		}
		diff.Changes = append(diff.Changes, message.DiffChange)
		for modifier, count := range map[rune]*int{
			'+': &diff.Summary.Added, '-': &diff.Summary.Removed,
			'M': &diff.Summary.Modified, 'T': &diff.Summary.TypeChanged,
			'U': &diff.Summary.MetadataChanged, '?': &diff.Summary.Damaged,
		} {
			if strings.ContainsRune(message.Modifier, modifier) {
				*count++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return diff, err
	}
	if !complete {
		return diff, fmt.Errorf("incomplete snapshot diff: no statistics received")
	}
	return diff, nil
}

func cmdDiff(c Config, from, to, path string, metadata, asJSON bool) error {
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	snaps, err := r.snapshots(c.Host)
	if err != nil {
		return err
	}
	a, err := snapshotAt(snaps, from)
	if err != nil {
		return err
	}
	b, err := snapshotAt(snaps, to)
	if err != nil {
		return err
	}
	args := []string{"diff", "--json", "--no-lock"}
	if metadata {
		args = append(args, "--metadata")
	}
	data, err := r.diffOutput(append(args, "--", a.ID, b.ID)...)
	if err != nil {
		return err
	}
	if path != "" {
		path = expand(path)
	}
	diff, err := decodeDiff(data, a.ID, b.ID, path)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(diff)
	}
	lipgloss.Println(accent.Render(short(a.ID)+" → "+short(b.ID)) + dim.Render(" · saved snapshots"))
	for _, change := range diff.Changes {
		lipgloss.Println(code.Render(change.Modifier) + "  " + tildify(change.Path))
	}
	if len(diff.Changes) == 0 {
		ok("no changes between these saved versions")
	}
	return nil
}

// restic diff can exit zero after errors reading subtrees; never call that a complete comparison.
func (r Restic) diffOutput(args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := r.cmd(args...)
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, &resticError{code: exitCode(err), msg: lastLine(stderr.String())}
	}
	if strings.TrimSpace(stderr.String()) != "" {
		return nil, fmt.Errorf("snapshot comparison may be incomplete: %s", clean(lastLine(stderr.String())))
	}
	return data, nil
}
