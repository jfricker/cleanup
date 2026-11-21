// Package cli defines the command-line interface: one flag table drives parsing,
// grouped --help output and the man page, so they can't drift apart (SPEC CLI-1…6).
package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/jfricker/cleanup/internal/config"
)

// Mode is dry run or delete.
type Mode int

// Modes.
const (
	DryRun Mode = iota
	Delete
)

// GroupBy is the initial TUI grouping.
type GroupBy int

// Groupings.
const (
	GroupCleaner GroupBy = iota
	GroupProject
)

// Options are the parsed arguments.
type Options struct {
	Mode           Mode
	Force          bool
	NoTUI          bool
	Root           string
	ConfigPath     string
	PrintConfig    bool
	ListCategories bool
	Only           []string
	Exclude        []string
	Retired        []string
	Venvs          bool
	Modules        bool
	AgeDays        int // 0 = use config
	IncludeDirty   bool
	ShowSkipped    bool
	GroupBy        GroupBy
	PrintMan       bool
	Help           bool
	Version        bool
}

// UsageError is a command-line mistake (exit status 2).
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, args ...any) error { return &UsageError{Msg: fmt.Sprintf(format, args...)} }

type flagDef struct {
	name, short, group, arg, help string
}

// Help groups, in display order.
const (
	gMode   = "Mode"
	gSelect = "Selection"
	gSafety = "Safety overrides"
	gOutput = "Output"
	gInfo   = "Information"
)

var groups = []string{gMode, gSelect, gSafety, gOutput, gInfo}

var flagDefs = []flagDef{
	{"dry-run", "n", gMode, "", "Preview only; delete nothing (default; the last of -n/-d wins)"},
	{"delete", "d", gMode, "", "Delete selected items (needs TUI confirmation or -f/--force)"},
	{"force", "f", gMode, "", "Skip the TUI and confirmation when deleting"},
	{"root", "r", gSelect, "DIR", "Directory to scan (default: project_root from config, else ~/Projects)"},
	{"config", "c", gSelect, "FILE", "Config file merged over the defaults (default ~/.config/cleanup/config.toml)"},
	{"only", "o", gSelect, "IDS", "Run only these cleaner ids (comma-separated or repeated)"},
	{"exclude", "x", gSelect, "IDS", "Skip these cleaner ids (comma-separated or repeated)"},
	{"venvs", "v", gSelect, "", "Python: include marker-verified virtualenvs"},
	{"modules", "m", gSelect, "", "Node: include node_modules"},
	{"retired", "u", gSelect, "IDS", "Treat these configured tool ids as retired for this run"},
	{"age", "a", gSelect, "DAYS", "Minimum idle age for worktrees and retired tools (default from config)"},
	{"include-dirty", "D", gSafety, "", "UNSAFE: also delete git trees with uncommitted changes, untracked files or an unreachable detached HEAD"},
	{"no-tui", "T", gOutput, "", "Plain-text output instead of the interactive TUI"},
	{"show-skipped", "s", gOutput, "", "Show skipped and protected items"},
	{"group-by", "g", gOutput, "MODE", "Initial TUI grouping: cleaner or project (default cleaner)"},
	{"list-categories", "l", gInfo, "", "List cleaners and retired tools, then exit"},
	{"print-config", "P", gInfo, "", "Print the default config, then exit"},
	{"print-man", "M", gInfo, "", "Print the man page (roff), then exit"},
	{"help", "h", gInfo, "", "Print help, then exit"},
	{"version", "V", gInfo, "", "Print the version, then exit"},
}

const removedY = "-y was removed: use -d/--delete to delete (by convention -y means \"assume yes\")"

type modeFlag struct {
	target *Mode
	value  Mode
}

func (m *modeFlag) String() string {
	return strconv.FormatBool(m.target != nil && *m.target == m.value)
}
func (m *modeFlag) Set(s string) error {
	if b, err := strconv.ParseBool(s); err == nil && b {
		*m.target = m.value
	}
	return nil
}
func (m *modeFlag) Type() string { return "bool" }

type listFlag struct{ values *[]string }

func (l *listFlag) String() string { return strings.Join(*l.values, ",") }
func (l *listFlag) Set(s string) error {
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(*l.values, p) {
			*l.values = append(*l.values, p)
		}
	}
	return nil
}
func (l *listFlag) Type() string { return "list" }

type removedFlag struct{}

func (removedFlag) String() string   { return "false" }
func (removedFlag) Set(string) error { return errors.New(removedY) }
func (removedFlag) Type() string     { return "bool" }

// Parse parses args (without the program name).
func Parse(args []string) (Options, error) {
	o := Options{}
	fs := pflag.NewFlagSet("cleanup", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.SortFlags = false
	var groupBy string
	var age int
	for _, d := range flagDefs {
		switch d.name {
		case "dry-run":
			fs.VarPF(&modeFlag{&o.Mode, DryRun}, d.name, d.short, d.help).NoOptDefVal = "true"
		case "delete":
			fs.VarPF(&modeFlag{&o.Mode, Delete}, d.name, d.short, d.help).NoOptDefVal = "true"
		case "force":
			fs.BoolVarP(&o.Force, d.name, d.short, false, d.help)
		case "root":
			fs.StringVarP(&o.Root, d.name, d.short, "", d.help)
		case "config":
			fs.StringVarP(&o.ConfigPath, d.name, d.short, "", d.help)
		case "only":
			fs.VarP(&listFlag{&o.Only}, d.name, d.short, d.help)
		case "exclude":
			fs.VarP(&listFlag{&o.Exclude}, d.name, d.short, d.help)
		case "venvs":
			fs.BoolVarP(&o.Venvs, d.name, d.short, false, d.help)
		case "modules":
			fs.BoolVarP(&o.Modules, d.name, d.short, false, d.help)
		case "retired":
			fs.VarP(&listFlag{&o.Retired}, d.name, d.short, d.help)
		case "age":
			fs.IntVarP(&age, d.name, d.short, 0, d.help)
		case "include-dirty":
			fs.BoolVarP(&o.IncludeDirty, d.name, d.short, false, d.help)
		case "no-tui":
			fs.BoolVarP(&o.NoTUI, d.name, d.short, false, d.help)
		case "show-skipped":
			fs.BoolVarP(&o.ShowSkipped, d.name, d.short, false, d.help)
		case "group-by":
			fs.StringVarP(&groupBy, d.name, d.short, "cleaner", d.help)
		case "list-categories":
			fs.BoolVarP(&o.ListCategories, d.name, d.short, false, d.help)
		case "print-config":
			fs.BoolVarP(&o.PrintConfig, d.name, d.short, false, d.help)
		case "print-man":
			fs.BoolVarP(&o.PrintMan, d.name, d.short, false, d.help)
		case "help":
			fs.BoolVarP(&o.Help, d.name, d.short, false, d.help)
		case "version":
			fs.BoolVarP(&o.Version, d.name, d.short, false, d.help)
		}
	}
	yf := fs.VarPF(removedFlag{}, "yes", "y", "removed")
	yf.NoOptDefVal, yf.Hidden = "true", true

	if err := fs.Parse(args); err != nil {
		if strings.Contains(err.Error(), removedY) {
			return o, &UsageError{Msg: removedY}
		}
		return o, &UsageError{Msg: err.Error()}
	}
	switch strings.ToLower(groupBy) {
	case "cleaner":
		o.GroupBy = GroupCleaner
	case "project":
		o.GroupBy = GroupProject
	default:
		return o, usagef("invalid --group-by %q (cleaner or project)", groupBy)
	}
	if fs.Changed("age") {
		if age < 1 {
			return o, usagef("--age must be at least 1 day (retired tools are always age-gated)")
		}
		o.AgeDays = age
	}
	if rest := fs.Args(); len(rest) > 0 {
		if len(rest) > 1 || o.Root != "" {
			return o, usagef("unexpected argument %q", rest[len(rest)-1])
		}
		o.Root = rest[0]
	}
	return o, nil
}

// ValidateIDs checks --only, --exclude and --retired against the config (CLI-4).
func ValidateIDs(o Options, cfg *config.Config) error {
	cleaners, tools := cfg.CleanerIDs(), cfg.ToolIDs()
	check := func(flag string, got, valid []string) error {
		for _, id := range got {
			if !slices.Contains(valid, id) {
				return usagef("unknown id %q for %s (valid: %s)", id, flag, strings.Join(valid, ", "))
			}
		}
		return nil
	}
	return errors.Join(check("--only", o.Only, cleaners), check("--exclude", o.Exclude, cleaners),
		check("--retired", o.Retired, tools))
}

// Help writes grouped help text.
func Help(w io.Writer, version string) {
	fmt.Fprintf(w, "cleanup %s: reclaim disk space from regenerable dev artifacts, stale git\n", version)
	fmt.Fprintln(w, "worktrees and retired tools' leftovers, without deleting work.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: cleanup [OPTIONS] [ROOT]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Runs as a dry run unless -d/--delete is given.")
	for _, g := range groups {
		fmt.Fprintf(w, "\n%s:\n", g)
		for _, d := range flagDefs {
			if d.group != g {
				continue
			}
			left := fmt.Sprintf("  -%s, --%s", d.short, d.name)
			if d.arg != "" {
				left += " " + d.arg
			}
			fmt.Fprintf(w, "%-28s %s\n", left, d.help)
		}
	}
	fmt.Fprintln(w, `
Examples:
  cleanup                         Interactive dry run over ~/Projects
  cleanup -d -v -m                Review and delete, including venvs and node_modules
  cleanup -T -d -f -o python,rust Delete Python caches and Rust targets without prompting
  cleanup -u cursor,zed           Preview leftovers of tools you no longer use
  cleanup -P > ~/.config/cleanup/config.toml
  cleanup -M | man -l -

Exit status: 0 success, 1 some deletions failed, 2 usage/config/root error, 130 interrupted.`)
}
