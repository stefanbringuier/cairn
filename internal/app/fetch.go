package app

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ponytail: pinned versions + hashes; bump all three together when upgrading.
const (
	resticVersion = "0.19.1"
	rcloneVersion = "1.75.2"
)

// pinned are the SHA256s from each release's SHA256SUMS, embedded so a tampered release can't swap both.
var pinned = map[string]string{
	"restic_0.19.1_linux_amd64.bz2":  "f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c",
	"restic_0.19.1_linux_arm64.bz2":  "a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465",
	"rclone-v1.75.2-linux-amd64.zip": "349ac8fba6ff65d6247043f1750cdcb518ec5d500ef91463a10d37c0ccdf3702",
	"rclone-v1.75.2-linux-arm64.zip": "7e1e8d69654941b7b7df84ee74c5f7fb09cce7d5496947fb7bf16b55ff427d10",
}

// tool finds restic/rclone on PATH (restic must be >= 0.17), else in cairn's bin dir, else downloads it.
func tool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil && (name != "restic" || resticOK(p)) {
		return p, nil
	}
	local := filepath.Join(dataDir(), "bin", name)
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return "", fmt.Errorf("%s not found: install it with your package manager", name)
	}
	fmt.Fprintf(os.Stderr, "cairn: downloading %s into %s\n", name, filepath.Dir(local))
	return local, download(name, local)
}

// resticOK: --insecure-no-password needs restic 0.17+.
func resticOK(bin string) bool {
	out, err := exec.Command(bin, "version").Output() // #nosec G204 -- a restic found on PATH
	var major, minor int
	if err != nil {
		return false
	}
	if _, err := fmt.Sscanf(string(out), "restic %d.%d", &major, &minor); err != nil {
		return false
	}
	return major > 0 || minor >= 17
}

func download(name, dest string) error {
	var url, file string
	if name == "restic" {
		file = fmt.Sprintf("restic_%s_linux_%s.bz2", resticVersion, runtime.GOARCH)
		url = "https://github.com/restic/restic/releases/download/v" + resticVersion + "/" + file
	} else {
		file = fmt.Sprintf("rclone-v%s-linux-%s.zip", rcloneVersion, runtime.GOARCH)
		url = "https://github.com/rclone/rclone/releases/download/v" + rcloneVersion + "/" + file
	}
	blob, err := get(url)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(blob); hex.EncodeToString(got[:]) != pinned[file] {
		return fmt.Errorf("checksum mismatch for %s: refusing to install it", file)
	}
	var bin io.Reader
	if name == "rclone" {
		if bin, err = unzipFile(blob, "/rclone"); err != nil {
			return err
		}
	} else {
		bin = bzip2.NewReader(bytes.NewReader(blob))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), name+".*") // unique: a timer and a CLI run may download at once
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, io.LimitReader(bin, 512<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o700) // #nosec G302 -- it's an executable, owner-only
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), dest)
}

var httpClient = &http.Client{Timeout: 10 * time.Minute}

func get(url string) ([]byte, error) {
	resp, err := httpClient.Get(url) // #nosec G107 -- URL built from pinned constants
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func unzipFile(blob []byte, suffix string) (io.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, suffix) {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("%s not found in archive", suffix)
}
