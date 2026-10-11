// Command cairn manages snapshots and full-machine backups.
package main

import (
	"os"

	"github.com/stefanbringuier/cairn/internal/app"
)

var version = "dev" // Set by GoReleaser via -ldflags.

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
