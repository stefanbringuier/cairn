package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type cloudTransport func(*http.Request) (*http.Response, error)

func (f cloudTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func cloudFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("PATH", dir)
	return dir
}

func cloudTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o700); err != nil { // #nosec G306 -- executable test fixture, owner-only
		t.Fatal(err)
	}
}

func TestCloudSetupBootstrapsCheckedRclone(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("automatic download requires supported Linux architecture")
	}
	dir := cloudFixture(t)
	log := filepath.Join(dir, "invocation")
	t.Setenv("CAIRN_TEST_LOG", log)
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	f, err := w.Create("rclone-test/rclone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$CAIRN_TEST_LOG\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	file := "rclone-v" + rcloneVersion + "-linux-" + runtime.GOARCH + ".zip"
	originalClient, originalHash := httpClient, pinned[file]
	t.Cleanup(func() { httpClient, pinned[file] = originalClient, originalHash })
	hash := sha256.Sum256(archive.Bytes())
	pinned[file] = hex.EncodeToString(hash[:])
	requests := 0
	httpClient = &http.Client{Transport: cloudTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if !strings.HasSuffix(req.URL.Path, file) {
			t.Fatalf("unexpected download: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive.Bytes()))}, nil
	})}
	if got := Run([]string{"cloud", "setup"}, "test"); got != 0 {
		t.Fatalf("setup exit %d", got)
	}
	if b, err := os.ReadFile(log); err != nil || string(b) != "config\n" { // #nosec G304 -- isolated test fixture
		t.Fatalf("configuration invocation: %q %v", b, err)
	}
	if err := cmdCloudSetup(); err != nil || requests != 1 {
		t.Fatalf("private tool reuse: %v, downloads %d", err, requests)
	}
	if _, err := os.Stat(configFile()); !os.IsNotExist(err) {
		t.Fatal("cloud setup wrote cairn configuration")
	}
	// A corrupt archive must never be installed or executed.
	if err := os.Remove(filepath.Join(dataDir(), "bin", "rclone")); err != nil {
		t.Fatal(err)
	}
	pinned[file] = strings.Repeat("0", 64)
	if err := cmdCloudSetup(); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corrupt download accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir(), "bin", "rclone")); !os.IsNotExist(err) {
		t.Fatal("corrupt tool installed")
	}
}

func TestCloudSetupRejectsArgumentsAndReportsFailure(t *testing.T) {
	dir := cloudFixture(t)
	cloudTool(t, dir, "rclone", "exit 1\n")
	for _, args := range [][]string{{"cloud"}, {"cloud", "other"}, {"cloud", "setup", "extra"}, {"cloud", "setup", "-y"}, {"cloud", "setup", "--json"}} {
		if got := Run(args, "test"); got != exitNeedsYes {
			t.Errorf("%q: exit %d", args, got)
		}
	}
	if got := Run([]string{"cloud", "setup"}, "test"); got != 1 {
		t.Fatalf("rclone failure exit %d", got)
	}
}

func TestCloudSetupUsesExistingToolAndInheritedStreams(t *testing.T) {
	dir := cloudFixture(t)
	log := filepath.Join(dir, "configuration-environment")
	t.Setenv("RCLONE_CONFIG", log)
	cloudTool(t, dir, "rclone", `test "$*" = config || exit 1
read -r answer
printf '%s' "$answer" > "$RCLONE_CONFIG"
printf 'wizard output'
printf 'wizard notice' >&2
`)
	c := defaultConfig()
	c.Target, c.Repo = "/current", "/current"
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configFile())
	if err != nil {
		t.Fatal(err)
	}
	streams := make([]*os.File, 3)
	for i := range streams {
		streams[i], err = os.CreateTemp(dir, "stream")
		if err != nil {
			t.Fatal(err)
		}
		defer streams[i].Close()
	}
	if _, err := streams[0].WriteString("human answer\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := streams[0].Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	stdin, stdout, stderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = streams[0], streams[1], streams[2]
	defer func() { os.Stdin, os.Stdout, os.Stderr = stdin, stdout, stderr }()
	if err := cmdCloudSetup(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(log); err != nil || string(b) != "human answer" { // #nosec G304 -- isolated test fixture
		t.Fatalf("stdin/configuration environment: %q %v", b, err)
	}
	for i, want := range []string{"wizard output", "wizard notice"} {
		if _, err := streams[i+1].Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if b, err := io.ReadAll(streams[i+1]); err != nil || string(b) != want {
			t.Fatalf("wizard stream: %q %v", b, err)
		}
	}
	if b, err := os.ReadFile(configFile()); err != nil || !bytes.Equal(b, before) {
		t.Fatal("setup changed the current cairn configuration")
	}
}

func TestFailedCloudInitPreservesConfigurationAndTimers(t *testing.T) {
	for _, failure := range []string{"missing-remote", "listremotes", "connection", "init"} {
		for _, existingPassword := range []bool{false, true} {
			t.Run(failure+"/existing-password="+strconv.FormatBool(existingPassword), func(t *testing.T) {
				dir := cloudFixture(t)
				c := defaultConfig()
				c.Target, c.Repo = "/existing", "/existing"
				if err := c.save(); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(configFile())
				if err != nil {
					t.Fatal(err)
				}
				timer := filepath.Join(filepath.Dir(configDir()), "systemd", "user", "cairn-snapshot.timer")
				if err := os.MkdirAll(filepath.Dir(timer), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(timer, []byte("existing timer"), 0o600); err != nil {
					t.Fatal(err)
				}
				if existingPassword {
					if err := os.WriteFile(passwordFile(), []byte("existing password"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				rcloneScript := "printf 'demo:\\n'\n"
				switch failure {
				case "missing-remote":
					rcloneScript = "exit 0\n"
				case "listremotes":
					rcloneScript = "printf 'secret credential' >&2\nexit 1\n"
				}
				cloudTool(t, dir, "rclone", rcloneScript)
				resticScript := "if [ \"$1\" = version ]; then printf 'restic 0.19.1\\n'; exit; fi\nprintf 'secret credential' >&2\nexit 1\n"
				if failure == "init" {
					resticScript = "if [ \"$1\" = version ]; then printf 'restic 0.19.1\\n'; exit; fi\ncase \"$*\" in *'cat config'*) exit 10 ;; esac\nprintf 'secret credential' >&2\nexit 1\n"
				}
				cloudTool(t, dir, "restic", resticScript)
				for _, target := range []string{"demo:secret credential/path", "rclone:demo:secret credential/path"} {
					err := cmdInit(target, true, []string{dir}, true)
					if err == nil || strings.Contains(err.Error(), "secret credential") {
						t.Fatalf("failure leaked credentials or succeeded: %v", err)
					}
					if !strings.Contains(err.Error(), "cairn cloud setup") {
						t.Fatalf("missing guidance: %v", err)
					}
					if got, err := os.ReadFile(configFile()); err != nil || !bytes.Equal(got, before) {
						t.Fatalf("changed current configuration: %q %v", got, err)
					}
					if got, err := os.ReadFile(timer); err != nil || string(got) != "existing timer" { // #nosec G304 -- isolated test fixture
						t.Fatalf("changed existing timer: %q %v", got, err)
					}
					pw, err := os.ReadFile(passwordFile())
					if existingPassword && (err != nil || string(pw) != "existing password") {
						t.Fatalf("changed existing password: %q %v", pw, err)
					} else if !existingPassword && !os.IsNotExist(err) {
						t.Fatalf("failed init left a password: %q %v", pw, err)
					}
				}
			})
		}
	}
}

func TestFailedInitToolBootstrapRemovesNewPassword(t *testing.T) {
	dir := cloudFixture(t)
	cloudTool(t, dir, "rclone", "printf 'demo:\\n'\n")
	originalClient := httpClient
	t.Cleanup(func() { httpClient = originalClient })
	httpClient = &http.Client{Transport: cloudTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("tool download unavailable")
	})}
	if err := cmdInit("demo:path", true, nil, true); err == nil {
		t.Fatal("initialization succeeded without restic")
	}
	if _, err := os.Stat(passwordFile()); !os.IsNotExist(err) {
		t.Fatal("failed tool bootstrap left a new password")
	}
}
