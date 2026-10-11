package app

import (
	"os"
	"testing"
)

func TestFetchRestic(t *testing.T) {
	if os.Getenv("CAIRN_NET_TEST") == "" {
		t.Skip("set CAIRN_NET_TEST=1")
	}
	dest := t.TempDir() + "/restic"
	if err := download("restic", dest); err != nil {
		t.Fatal(err)
	}
	if !resticOK(dest) {
		t.Fatal("downloaded restic doesn't run")
	}
}
