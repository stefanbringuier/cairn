package app

import (
	"slices"
	"testing"
	"time"
)

func TestPickAndCandidates(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local)
	snaps := []Snap{ // oldest first, like restic
		{ID: "aa11", Time: now.Add(-48 * time.Hour), Paths: []string{"/home/u"}, Tags: []string{"snapshot"}},
		{ID: "123d00", Time: now.Add(-2 * time.Hour), Paths: []string{"/data"}, Tags: []string{"backup"}},
		{ID: "cc33", Time: now.Add(-1 * time.Hour), Paths: []string{"/home/u"}, Tags: []string{"snapshot"}},
		{ID: "dd440", Time: now.Add(-1 * time.Minute), Paths: []string{"/home/u/x"}, Tags: []string{"pre-restore"}},
		{ID: "dd441", Time: now, Paths: []string{"/home/u/x"}, Tags: []string{"pre-restore"}},
	}
	if c, _, _ := pick(snaps, "3d", now); !c.Equal(now.Add(-72 * time.Hour)) {
		t.Errorf("3d: %v", c)
	}
	if c, _, _ := pick(snaps, "2026-10-01", now); c.Format("2006-01-02 15:04:05") != "2026-10-01 23:59:59" {
		t.Errorf("date: %v", c)
	}
	if _, id, _ := pick(snaps, "123d", now); id != "123d00" {
		t.Errorf("a hex id must win over an age: %q", id)
	}
	if _, _, err := pick(snaps, "dd44", now); err == nil {
		t.Error("ambiguous id prefix must fail")
	}
	if _, _, err := pick(snaps, "99999999d", now); err == nil {
		t.Error("absurd ages must fail, not overflow")
	}
	ids := func(s []Snap) (out []string) {
		for _, x := range s {
			out = append(out, x.ID)
		}
		return out
	}
	if got := ids(candidates(snaps, "/home/u/x", now, "")); !slices.Equal(got, []string{"cc33", "aa11"}) {
		t.Errorf("latest: %v", got)
	}
	if got := ids(candidates(snaps, "/home/u/x", now.Add(-24*time.Hour), "")); !slices.Equal(got, []string{"aa11"}) {
		t.Errorf("cutoff: %v", got)
	}
	if got := ids(candidates(snaps, "/home/u/x", now, "dd440")); !slices.Equal(got, []string{"dd440"}) {
		t.Errorf("by id: %v", got)
	}
	if (Snap{Paths: []string{"/home/u"}}).covers("/home/user2") || !(Snap{Paths: []string{"/"}}).covers("/etc") {
		t.Error("covers must match whole path components")
	}
}
