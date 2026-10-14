package app

import (
	"strings"
	"testing"
)

func TestRepoForAndRedact(t *testing.T) {
	dir := cloudFixture(t)
	cloudTool(t, dir, "rclone", "printf 'g:\\n'\n")
	for in, want := range map[string]string{"/mnt/nas/c": "/mnt/nas/c", "~/b": home + "/b", "sftp:u@h:/x": "sftp:u@h:/x", "rclone:g:x": "rclone:g:x", "g:x": "rclone:g:x"} {
		if got, err := repoFor(in); err != nil || got != want {
			t.Errorf("%s: got %q %v", in, got, err)
		}
	}
	if got := redact("rest:https://me:s3cret@host:8000/repo"); strings.Contains(got, "s3cret") || !strings.Contains(got, "me@host") {
		t.Errorf("redact: %s", got)
	}
	if redact("/mnt/nas") != "/mnt/nas" {
		t.Error("paths must pass through")
	}
}
