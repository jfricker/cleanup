// Command cleanup reclaims disk space from regenerable development artifacts,
// stale git worktrees and leftovers of retired tools, without deleting work.
package main

import "os"

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
