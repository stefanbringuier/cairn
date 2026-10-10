package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
)

type progress struct {
	Percent    float64 `json:"percent_done"`
	FilesDone  int64   `json:"files_done"`
	BytesDone  uint64  `json:"bytes_done"`
	TotalBytes uint64  `json:"total_bytes"`
	ETA        int64   `json:"seconds_remaining"`
}

type summary struct {
	SnapshotID   string  `json:"snapshot_id"`
	FilesNew     int64   `json:"files_new"`
	FilesChanged int64   `json:"files_changed"`
	Files        int64   `json:"total_files_processed"`
	Added        uint64  `json:"data_added_packed"`
	Duration     float64 `json:"total_duration"`
}

func progressFile(kind string) string { return filepath.Join(stateDir(), kind+".progress") }

// systemPaths are read without root, so unreadable files there are expected, not a failure.
var systemPaths = []string{"/etc/", "/usr/share/keyrings/"}

// backupRun runs `restic backup --json`, streaming progress to the TUI's file (and the terminal).
// It returns the summary and the unreadable items restic reported.
func (r Restic) backupRun(kind string, args []string, tty bool) (summary, []string, error) {
	var sum summary
	cmd := r.cmd(args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return sum, nil, err
	}
	defer os.Remove(progressFile(kind))

	var unreadable []string
	var lastErr string
	var wg sync.WaitGroup
	wg.Go(func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			var m struct {
				Type    string `json:"message_type"`
				Item    string `json:"item"`
				Message string `json:"message"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				lastErr = sc.Text()
				continue
			}
			switch m.Type {
			case "error":
				unreadable = append(unreadable, m.Item)
			case "exit_error":
				lastErr = m.Message
			}
		}
	})
	sc := bufio.NewScanner(stdout)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var head struct {
			Type string `json:"message_type"`
		}
		if json.Unmarshal(line, &head) != nil {
			continue
		}
		switch head.Type {
		case "status":
			_ = os.WriteFile(progressFile(kind), line, 0o600) // best effort: only the TUI reads it
			if tty {
				var p progress
				_ = json.Unmarshal(line, &p)
				fmt.Fprintf(os.Stderr, "\r\033[K  %s %s", bar(p.Percent, 24), dim.Render(fmt.Sprintf("%s / %s  %d files  ~%s left",
					humanize.IBytes(p.BytesDone), humanize.IBytes(p.TotalBytes), p.FilesDone, time.Duration(p.ETA)*time.Second)))
			}
		case "summary":
			_ = json.Unmarshal(line, &sum)
		}
	}
	wg.Wait()
	if tty {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if err := cmd.Wait(); err != nil {
		return sum, unreadable, &resticError{code: exitCode(err), msg: lastErr}
	}
	return sum, unreadable, nil
}

// runJob is what `cairn snapshot|backup` and the timers run.
func runJob(c Config, kind string, tty bool) error {
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	release, err := lockJobs(tty)
	if err != nil {
		return err
	}
	defer release()

	paths := c.job(kind).Paths
	if kind == "backup" {
		writeManifests()
		paths = append(slices.Clone(paths), manifestDir()) // always ship the recipe, even if $HOME isn't listed
	}
	var existing []string
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			existing = append(existing, p)
		}
	}
	if len(existing) == 0 {
		return fmt.Errorf("none of the %s paths exist: %s", kind, strings.Join(paths, ", "))
	}
	args := slices.Concat([]string{"backup", "--json", "--host", c.Host, "--tag", kind, "--one-file-system", "--retry-lock", "10m"},
		c.excludeArgs(), []string{"--"}, existing)

	sum, unreadable, err := r.backupRun(kind, args, tty)
	partial := false
	switch codeOf(err) {
	case 0:
	case exitPartial:
		user := slices.DeleteFunc(slices.Clone(unreadable), func(p string) bool {
			return slices.ContainsFunc(systemPaths, func(s string) bool { return strings.HasPrefix(p, s) })
		})
		if len(user) == 0 {
			break
		}
		partial = true
		warn(fmt.Sprintf("%d file(s) couldn't be read, e.g. %s", len(user), tildify(user[0])))
	case exitNoRepo:
		if os.Getenv("INVOCATION_ID") != "" { // a systemd timer run with the NAS/USB disk away: not an error
			fmt.Printf("target %s offline, skipped\n", redact(c.Target))
			return nil
		}
		return err
	default:
		return err
	}
	ok(fmt.Sprintf("%s %s · %d files (%d new, %d changed) · +%s · %s", kind, short(sum.SnapshotID),
		sum.Files, sum.FilesNew, sum.FilesChanged, humanize.IBytes(sum.Added), time.Duration(sum.Duration*float64(time.Second)).Round(time.Second)))
	if n := len(unreadable); n > 0 && !partial {
		note(fmt.Sprintf("skipped %d root-only system files (expected without sudo)", n))
	}

	if err := r.forget(c, kind); err != nil {
		return err
	}
	if partial {
		return &resticError{code: exitPartial, msg: "some files couldn't be read"}
	}
	return nil
}

func prunedFile() string { return filepath.Join(stateDir(), "pruned") }

// forget applies retention; pruning (slow, takes an exclusive lock) happens at most about daily.
func (r Restic) forget(c Config, kind string) error {
	keep, err := keepFlags(c.job(kind).Keep)
	if err != nil {
		return err
	}
	base := []string{"forget", "--host", c.Host, "--group-by", "host", "--retry-lock", "30m"}
	fi, err := os.Stat(prunedFile())
	prune := err != nil || time.Since(fi.ModTime()) > 20*time.Hour
	if prune {
		if _, err := r.out(slices.Concat(base, []string{"--tag", "pre-restore", "--keep-within", "30d"})...); err != nil {
			return err
		}
	}
	args := slices.Concat(base, []string{"--tag", kind}, keep)
	if prune {
		args = append(args, "--prune")
	}
	if _, err := r.out(args...); err != nil {
		return err
	}
	if prune {
		return os.WriteFile(prunedFile(), nil, 0o600)
	}
	return nil
}

type JobStatus struct {
	Job
	Count   int        `json:"count"`
	Last    *time.Time `json:"last,omitempty"`
	LastID  string     `json:"last_id,omitempty"`
	Next    *time.Time `json:"next,omitempty"`
	Result  string     `json:"result,omitempty"` // ok | partial | failed (last timer run)
	Running bool       `json:"running"`
	Percent float64    `json:"percent,omitempty"`
	Stale   bool       `json:"stale"`
}

type Status struct {
	Target    string    `json:"target"`
	Host      string    `json:"host"`
	Online    bool      `json:"online"`
	Error     string    `json:"error,omitempty"`
	Scheduler bool      `json:"scheduler"`
	Snapshot  JobStatus `json:"snapshot"`
	Backup    JobStatus `json:"backup"`
}

func (s *Status) job(kind string) *JobStatus {
	if kind == "backup" {
		return &s.Backup
	}
	return &s.Snapshot
}

// live refreshes the cheap parts (systemd state, progress file) without touching the repo.
func (s *Status) live() {
	for _, kind := range []string{"snapshot", "backup"} {
		js := s.job(kind)
		js.Next, js.Running, js.Percent = nil, false, 0
		if u, ok := readUnits(kind); ok {
			if !u.Next.IsZero() {
				js.Next = &u.Next
			}
			js.Result, js.Running = u.Result, u.Running
		} else {
			s.Scheduler = false
		}
		if fi, err := os.Stat(progressFile(kind)); err == nil && time.Since(fi.ModTime()) < 10*time.Second {
			var p progress
			b, _ := os.ReadFile(progressFile(kind))
			_ = json.Unmarshal(b, &p)
			js.Running, js.Percent = true, p.Percent
		}
	}
}

func getStatus(c Config) Status {
	st := Status{Target: redact(c.Target), Host: c.Host, Scheduler: true}
	var snaps []Snap
	r, err := newRestic(c)
	if err == nil {
		snaps, err = r.snapshots(c.Host)
	}
	st.Online = err == nil
	if err != nil {
		st.Error = err.Error()
	}
	for _, kind := range []string{"snapshot", "backup"} {
		js := JobStatus{Job: *c.job(kind)}
		for _, s := range snaps {
			if s.kind() == kind {
				js.Count++
				js.Last, js.LastID = &s.Time, s.ShortID
			}
		}
		iv := interval(js.Every)
		js.Stale = iv > 0 && js.Last != nil && time.Since(*js.Last) > 2*iv
		*st.job(kind) = js
	}
	st.live()
	return st
}

func short(id string) string { return id[:min(8, len(id))] }
