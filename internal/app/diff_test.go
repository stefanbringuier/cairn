package app

import (
	"strings"
	"testing"
	"time"
)

func TestDiffRefusesExitZeroWithReadErrors(t *testing.T) {
	r := scriptedRestic(t, `printf '{"message_type":"statistics"}\n'
printf 'error: cannot read snapshot subtree\n' >&2
`)
	if _, err := r.diffOutput("diff", "--json", "--", "a", "b"); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("accepted an incomplete native diff: %v", err)
	}
}

func TestDiffFiltersPathsAndCountsModifiers(t *testing.T) {
	data := []byte(`{"message_type":"change","path":"/work/dog.mp4","modifier":"MU"}
{"message_type":"change","path":"/work/dog.mp4.old","modifier":"+"}
{"message_type":"change","path":"/work/folder/removed","modifier":"-"}
{"message_type":"change","path":"/work/folder/type","modifier":"T"}
{"message_type":"change","path":"/work/folder/damaged","modifier":"?"}
{"message_type":"statistics","changed_files":99}`)
	diff, err := decodeDiff(data, "from", "to", "/work/dog.mp4")
	if err != nil || len(diff.Changes) != 1 || diff.Summary.Modified != 1 || diff.Summary.MetadataChanged != 1 || diff.Summary.Added != 0 {
		t.Fatalf("file filter: %+v, %v", diff, err)
	}
	diff, err = decodeDiff(data, "from", "to", "/work/folder")
	if err != nil || len(diff.Changes) != 3 || diff.Summary.Removed != 1 || diff.Summary.TypeChanged != 1 || diff.Summary.Damaged != 1 {
		t.Fatalf("folder filter: %+v, %v", diff, err)
	}
	diff, err = decodeDiff(data, "from", "to", "/missing")
	if err != nil || diff.Changes == nil || len(diff.Changes) != 0 {
		t.Fatalf("empty filter: %+v, %v", diff, err)
	}
	for _, invalid := range []string{`not json`, `{"message_type":"change","path":"/work/dog.mp4","modifier":"M"}`} {
		if _, err := decodeDiff([]byte(invalid), "from", "to", ""); err == nil {
			t.Errorf("accepted incomplete or invalid diff: %s", invalid)
		}
	}
}

func TestDiffSnapshotSelectionExcludesSafetyUnlessExplicit(t *testing.T) {
	now := time.Now()
	snaps := []Snap{
		{ID: strings.Repeat("b", 64), Time: now.Add(-time.Hour), Tags: []string{"snapshot"}},
		{ID: strings.Repeat("a", 64), Time: now.Add(-48 * time.Hour), Tags: []string{"snapshot"}},
		{ID: strings.Repeat("c", 64), Time: now.Add(-time.Minute), Tags: []string{"pre-restore"}},
	}
	for at, want := range map[string]string{"latest": snaps[0].ID, "1d": snaps[1].ID, "cccccccc": snaps[2].ID} {
		got, err := snapshotAt(snaps, at)
		if err != nil || got.ID != want {
			t.Errorf("%s: %s, %v; want %s", at, got.ID, err, want)
		}
	}
	if _, err := snapshotAt(snaps, "10d"); err == nil {
		t.Error("selection before the first snapshot must fail")
	}
}
