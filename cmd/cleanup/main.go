// Command cleanup reclaims disk space from regenerable development artifacts,
// stale git worktrees and leftovers of retired tools, without deleting work.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], realEnv()))
}
