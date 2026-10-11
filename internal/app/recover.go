package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type Recovery struct {
	Snapshot    string `json:"snapshot"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// recoverCopy publishes a completed export without replacing even a racing destination.
func recoverCopy(r Restic, s Snap, n Node, source, destination string) error {
	if n.Type != "file" && n.Type != "dir" {
		return fmt.Errorf("recover supports regular files and folders, not %s", n.Type)
	}
	return publishCopy(r, s, n, source, destination, false)
}

// preserveMetadata keeps native restore semantics, including deleted symlinks and executables.
func publishCopy(r Restic, s Snap, n Node, source, destination string, preserveMetadata bool) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination already exists: %s; choose a new path", tildify(destination))
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".cairn-recover-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	copyPath := filepath.Join(staging, "copy")
	if preserveMetadata && source != "/" {
		copyPath = filepath.Join(staging, filepath.Base(source))
		_, err = r.out(includeArgs(s, source, staging, "--no-lock")...)
	} else if n.Type == "dir" {
		_, err = r.out("restore", "--no-lock", "--target", copyPath, "--", s.ID+":"+source)
	} else {
		_, err = r.out("dump", "--no-lock", "--target", copyPath, "--", s.ID, source)
	}
	if err != nil {
		return err
	}
	if !preserveMetadata && n.Type == "file" {
		if err := os.Chmod(copyPath, 0o600); err != nil {
			return err
		}
	}
	if err := unix.Renameat2(unix.AT_FDCWD, copyPath, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("cannot publish recovery without replacement: %w", err)
	}
	return nil
}

func cmdRecover(c Config, path, at, to string, asJSON bool) error {
	path = expand(path)
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	s, n, err := resolve(r, c, path, at)
	if err != nil {
		return err
	}
	created := ""
	if to == "" {
		created, err = os.MkdirTemp("", "cairn-recovered-")
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if name == "/" {
			name = "root"
		}
		to = filepath.Join(created, name)
	}
	to = expand(to)
	if err := recoverCopy(r, s, n, path, to); err != nil {
		if created != "" {
			_ = os.RemoveAll(created)
		}
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(Recovery{Snapshot: s.ID, Source: path, Destination: to})
	}
	ok("recovered " + tildify(path) + " from " + s.ShortID + " → " + tildify(to))
	note("new copy only; current files were not replaced")
	return nil
}
