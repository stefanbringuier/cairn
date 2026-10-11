package app

import (
	"slices"
	"strings"
	"testing"
)

func TestKeepFlags(t *testing.T) {
	got, err := keepFlags("8, 24h,7d,4w,12m,2y")
	want := []string{"--keep-last", "8", "--keep-hourly", "24", "--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "12", "--keep-yearly", "2"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "7x", "d", "0d", "-1d", "0"} {
		if _, err := keepFlags(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestEditListAndSet(t *testing.T) {
	l := editList([]string{"a"}, "+b", strings.ToUpper)
	if l = editList(l, "-a", func(s string) string { return s }); !slices.Equal(l, []string{"B"}) {
		t.Errorf("edit: %v", l)
	}
	c := defaultConfig()
	if c.set("backup.keep", "nope") == nil || c.set("bogus", "x") == nil || c.set("snapshot.paths", "-"+home) == nil {
		t.Error("bad sets should fail")
	}
}
