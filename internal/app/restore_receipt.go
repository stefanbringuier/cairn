package app

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// Only restore accepts a full safety receipt across hosts. This keeps printed
// undo commands usable after rebuild changes the configured host. Ordinary
// snapshots, partial IDs, latest/time selection, recover and history keep their
// existing host scope; repository/password selection is never changed.
func resolveRestore(r Restic, c Config, path, at string) (Snap, Node, error) {
	decoded, err := hex.DecodeString(at)
	if err != nil || len(decoded) != 32 {
		return resolve(r, c, path, at)
	}
	local, err := r.snapshots(c.Host)
	if err != nil {
		return Snap{}, Node{}, err
	}
	count := 0
	for _, s := range local {
		if s.ID == at {
			count++
		}
	}
	if count > 1 {
		return Snap{}, Node{}, fmt.Errorf("ambiguous full snapshot ID")
	}
	if count == 1 {
		return resolve(r, c, path, at)
	}
	receipts, err := r.snapshots("", "pre-restore")
	if err != nil {
		return Snap{}, Node{}, err
	}
	var receipt Snap
	count = 0
	for _, s := range receipts {
		if s.ID == at {
			receipt, count = s, count+1
		}
	}
	if count != 1 || len(receipt.Tags) != 1 || receipt.Tags[0] != "pre-restore" ||
		len(receipt.Paths) != 1 || receipt.Paths[0] != filepath.Clean(path) || path != filepath.Clean(path) {
		return Snap{}, Node{}, fmt.Errorf("full ID is not an unambiguous safety snapshot for this exact path in the selected repository")
	}
	nodes, err := r.ls(receipt.ID, path)
	if err != nil {
		return Snap{}, Node{}, err
	}
	if len(nodes) == 0 || nodes[0].Path != path {
		return Snap{}, Node{}, fmt.Errorf("exact path missing from safety snapshot")
	}
	note("explicit pre-restore safety snapshot from host " + clean(receipt.Hostname) + "; configured host unchanged")
	return receipt, nodes[0], nil
}
