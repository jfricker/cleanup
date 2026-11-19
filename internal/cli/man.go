package cli

import (
	"fmt"
	"strings"
)

// Man returns the cleanup(1) roff man page, generated from the same flag table as
// --help (SPEC DOC-1).
func Man(version, date string) string {
	var b strings.Builder
	fmt.Fprintf(&b, ".TH CLEANUP 1 %q %q \"User Commands\"\n", date, "cleanup "+version)
	b.WriteString(".SH NAME\ncleanup \\- reclaim disk space from dev artifacts, stale worktrees and retired tools without deleting work\n")
	b.WriteString(".SH SYNOPSIS\n\\fBcleanup\\fR [\\fIOPTIONS\\fR] [\\fIROOT\\fR]\n")
	b.WriteString(".SH DESCRIPTION\n")
	b.WriteString("\\fBcleanup\\fR scans one root (default \\fI~/Projects\\fR) once, proposes regenerable build artifacts, ")
	b.WriteString("stale linked git worktrees and leftovers of tools you have marked as retired, and inspects every ")
	b.WriteString("candidate before anything can be deleted. It runs as a dry run unless \\fB\\-d\\fR is given.\n")
	for _, g := range groups {
		fmt.Fprintf(&b, ".SH %s\n", strings.ToUpper(roff(g)))
		for _, d := range flagDefs {
			if d.group != g {
				continue
			}
			arg := ""
			if d.arg != "" {
				arg = " \\fI" + d.arg + "\\fR"
			}
			fmt.Fprintf(&b, ".TP\n\\fB\\-%s\\fR, \\fB\\-\\-%s\\fR%s\n%s\n", d.short, roff(d.name), arg, roff(d.help))
		}
	}
	b.WriteString(manSections)
	return b.String()
}

func roff(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "-", `\-`)
}

const manSections = `.SH SAFETY
Every candidate passes one inspection step that fails closed: any error skips the item.
.IP \(bu 2
Git\-tracked files are never deleted.
.IP \(bu 2
A git tree is skipped when it has uncommitted changes, untracked files, an unreachable detached HEAD,
lock state, or (in full clones) stashes or commits on no remote. Ignored files are listed but do not block.
.IP \(bu 2
A folder that contains a skipped or protected item is itself skipped.
.IP \(bu 2
Symlinks are never followed; nothing outside the canonical root is touched. \fI/\fR, the home directory and
its ancestors, \fI~/Documents\fR, \fI~/Desktop\fR, \fI~/Downloads\fR, \fI~/Library\fR (iCloud, CloudStorage)
and system folders are refused as roots.
.IP \(bu 2
Git runs with fsmonitor, hooks and filter drivers disabled and a scrubbed environment.
.IP \(bu 2
Each item is re\-checked immediately before deletion.
.SH CONFIGURATION
Defaults are built in. \fI$XDG_CONFIG_HOME/cleanup/config.toml\fR (default \fI~/.config/cleanup/config.toml\fR)
or \fB\-c\fR \fIFILE\fR is merged over them. The file must contain \fBversion = 1\fR; unknown keys and
over\-broad patterns are errors. Start from \fBcleanup \-P\fR.
.SH FILES
.TP
\fI$XDG_STATE_HOME/cleanup/runs.jsonl\fR
Append\-only log of every \fB\-d\fR run (default \fI~/.local/state/cleanup/runs.jsonl\fR).
.SH EXIT STATUS
.TP
\fB0\fR
Success, including a dry run.
.TP
\fB1\fR
One or more deletions failed.
.TP
\fB2\fR
Usage, config or root\-guard error.
.TP
\fB130\fR
Interrupted.
.SH EXAMPLES
.TP
\fBcleanup\fR
Interactive dry run over \fI~/Projects\fR.
.TP
\fBcleanup \-d \-v \-m\fR
Review and delete, including virtualenvs and \fInode_modules\fR.
.TP
\fBcleanup \-T \-d \-f \-o python,rust\fR
Delete Python caches and Rust targets without prompting.
`
