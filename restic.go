package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Exit codes: restic's (3, 10, 12) pass through; 2 is ours for "needs -y / bad usage".
const (
	exitNeedsYes = 2
	exitPartial  = 3
	exitNoRepo   = 10
	exitBadPass  = 12
)

type Restic struct {
	bin, repo, rclone string
}

func newRestic(c Config) (Restic, error) {
	bin, err := tool("restic")
	if err != nil {
		return Restic{}, err
	}
	r := Restic{bin: bin, repo: c.Repo}
	if strings.HasPrefix(c.Repo, "rclone:") {
		if r.rclone, err = tool("rclone"); err != nil {
			return Restic{}, err
		}
	}
	return r, nil
}

// cmd builds a restic command. Callers put "--" before positional args so repo-supplied
// paths can never be read as options.
func (r Restic) cmd(args ...string) *exec.Cmd {
	var base []string
	if _, err := os.Stat(passwordFile()); err == nil {
		base = append(base, "--password-file", passwordFile())
	} else {
		base = append(base, "--insecure-no-password")
	}
	if r.rclone != "" {
		base = append(base, "-o", "rclone.program="+r.rclone)
	}
	cmd := exec.Command(r.bin, append(base, args...)...) // #nosec G204 G702 -- fixed binary, args are flags or follow "--"
	// Drop the user's RESTIC_* (they'd fight our flags); pass the repo via env so URL credentials stay out of argv.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(e string) bool { return strings.HasPrefix(e, "RESTIC_") })
	cmd.Env = append(cmd.Env, "RESTIC_REPOSITORY="+r.repo, "RESTIC_PROGRESS_FPS=2")
	return cmd
}

// out runs restic and returns stdout; the error carries stderr's last line.
func (r Restic) out(args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := r.cmd(args...)
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return b, &resticError{code: exitCode(err), msg: lastLine(stderr.String())}
	}
	return b, nil
}

type resticError struct {
	code int
	msg  string
}

func (e *resticError) Error() string {
	switch e.code {
	case exitNoRepo:
		return "target offline or no repository there"
	case exitBadPass:
		return "wrong password for this repository"
	}
	return fmt.Sprintf("restic: %s (exit %d)", clean(e.msg), e.code)
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func codeOf(err error) int {
	var re *resticError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &re):
		return re.code
	}
	return 1
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

type Snap struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
}

func (s Snap) kind() string {
	for _, t := range []string{"snapshot", "backup", "pre-restore"} {
		if slices.Contains(s.Tags, t) {
			return t
		}
	}
	return "other"
}

// covers reports whether path p lies inside one of the snapshot's source paths.
func (s Snap) covers(p string) bool {
	for _, sp := range s.Paths {
		if p == sp || strings.HasPrefix(p, strings.TrimSuffix(sp, "/")+"/") {
			return true
		}
	}
	return false
}

// snapshots lists snapshots oldest first; host "" means every host.
func (r Restic) snapshots(host string, tags ...string) ([]Snap, error) {
	args := []string{"snapshots", "--json", "--no-lock"}
	if host != "" {
		args = append(args, "--host", host)
	}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	b, err := r.out(args...)
	if err != nil {
		return nil, err
	}
	var snaps []Snap
	return snaps, json.Unmarshal(b, &snaps)
}

type Node struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
}

// ls returns the node at dir (first) followed by its direct children; empty if dir isn't there.
func (r Restic) ls(snap, dir string) ([]Node, error) {
	b, err := r.out("ls", "--json", "--no-lock", "--", snap, dir)
	if err != nil {
		return nil, err
	}
	var nodes []Node
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 16<<20)
	for sc.Scan() {
		var n Node
		if json.Unmarshal(sc.Bytes(), &n) == nil && n.Path != "" {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

var resticSchemes = []string{"sftp:", "rest:", "s3:", "b2:", "azure:", "gs:", "swift:", "rclone:"}

// repoFor turns what the user typed into a restic repository string.
func repoFor(target string) (string, error) {
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "~") || strings.HasPrefix(target, ".") {
		return expand(target), nil
	}
	for _, s := range resticSchemes {
		if strings.HasPrefix(target, s) {
			return target, nil
		}
	}
	name, _, ok := strings.Cut(target, ":")
	if !ok {
		return expand(target), nil
	}
	rclone, err := tool("rclone")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(rclone, "listremotes").Output() // #nosec G204 -- our rclone binary
	if err != nil {
		return "", fmt.Errorf("rclone listremotes: %w", err)
	}
	if !slices.Contains(strings.Fields(string(out)), name+":") {
		return "", fmt.Errorf("no rclone remote %q: create it with `%s config` (e.g. Google Drive), then rerun", name, rclone)
	}
	return "rclone:" + target, nil
}

// redact hides credentials in URL-style targets (rest:https://user:pass@host/...).
func redact(target string) string {
	scheme, rest, ok := strings.Cut(target, ":")
	if u, err := url.Parse(rest); ok && err == nil && u.User != nil {
		u.User = url.User(u.User.Username())
		return scheme + ":" + u.String()
	}
	return target
}

// globEscape makes a path literal for restic --include/--exclude/find patterns.
func globEscape(p string) string {
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(`\[]*?`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
