package plain

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jfricker/cleanup/internal/onboard"
	"github.com/jfricker/cleanup/internal/scan"
)

// Onboard drives f with line-by-line prompts (ONB-1, ONB-3, TOOL-3/4/7, NFR-10).
//
// Contract (shared with tui.RunOnboard): Onboard never writes the config. When the
// user confirms the review step it calls write exactly once and prints the text it
// returns, then returns (0, nil); if write fails it returns (2, err). Cancelling
// (EOF on r, ctx cancellation, answering "n" at the confirmation) returns
// (onboard.ExitCancelled, nil) and write is never called. Output contains no ANSI
// escapes. The probe in step 3 runs synchronously, so EOF typed during the probe is
// noticed when it finishes; ctx cancellation stops it (TOOL-6).
func Onboard(ctx context.Context, r io.Reader, w io.Writer, f *onboard.Flow, write onboard.WriteFunc) (int, error) {
	lines, done := readLines(r)
	defer close(done)
	o := &onboarder{ctx: ctx, lines: lines, w: w, f: f}
	if pw := f.PathWarning(); pw != "" {
		fmt.Fprintf(w, "Warning: %s\n\n", pw)
	}
	fmt.Fprintln(w, "cleanup setup (answer ? for help, b to go back, Ctrl+C or EOF to cancel)")
	for {
		var ok bool
		switch f.Step() {
		case onboard.Root:
			ok = o.root()
		case onboard.Protected:
			ok = o.protected()
		case onboard.Tools:
			ok = o.tools()
		case onboard.Summary:
			var finished bool
			ok, finished = o.confirm()
			if ok && finished {
				return o.finish(write)
			}
		}
		if !ok {
			fmt.Fprintln(w, "\nCancelled; nothing was written.")
			return onboard.ExitCancelled, nil
		}
	}
}

func (o *onboarder) finish(write onboard.WriteFunc) (int, error) {
	summary, err := write()
	if err != nil {
		fmt.Fprintf(o.w, "error: %v\n", err)
		return 2, err
	}
	fmt.Fprintf(o.w, "\n%s", summary)
	if !strings.HasSuffix(summary, "\n") {
		fmt.Fprintln(o.w)
	}
	return 0, nil
}

// readLines feeds lines from r into a channel that is closed on EOF or error. The
// goroutine stops delivering once done is closed.
func readLines(r io.Reader) (<-chan string, chan struct{}) {
	ch := make(chan string)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			select {
			case ch <- strings.TrimSpace(sc.Text()):
			case <-done:
				return
			}
		}
	}()
	return ch, done
}

type onboarder struct {
	ctx   context.Context
	lines <-chan string
	w     io.Writer
	f     *onboard.Flow
}

// ask prints prompt and returns the next line; ok is false on EOF or cancellation.
func (o *onboarder) ask(prompt string) (string, bool) {
	fmt.Fprint(o.w, prompt)
	select {
	case <-o.ctx.Done():
		return "", false
	case l, ok := <-o.lines:
		return l, ok
	}
}

func (o *onboarder) root() bool {
	for {
		def := o.f.RootDisplay()
		fmt.Fprintln(o.w, "\nStep 1 of 4: project root")
		ans, ok := o.ask(fmt.Sprintf("Directory to scan [%s]: ", def))
		if !ok {
			return false
		}
		switch ans {
		case "?":
			fmt.Fprintln(o.w, "  The directory cleanup scans: an absolute path or one starting with ~.")
			continue
		case "b":
			fmt.Fprintln(o.w, "  This is the first step.")
			continue
		case "":
			ans = def
		}
		if err := o.f.SetRoot(ans); err != nil {
			fmt.Fprintf(o.w, "  error: %v\n", err)
			continue
		}
		o.f.Next()
		return true
	}
}

func (o *onboarder) protected() bool {
	fmt.Fprintln(o.w, "\nStep 2 of 4: protected directories")
	if n := o.f.Notice(); n != "" {
		fmt.Fprintf(o.w, "  note: %s\n", n)
	}
	for {
		prot := o.f.Protected()
		if len(prot) == 0 {
			fmt.Fprintln(o.w, "Protected directories (never scanned or cleaned): none")
		} else {
			fmt.Fprintln(o.w, "Protected directories (never scanned or cleaned):")
			for i, p := range prot {
				fmt.Fprintf(o.w, "  %d. %s\n", i+1, p)
			}
		}
		ans, ok := o.ask("a <path> add, d <n> remove, Enter continue [continue]: ")
		if !ok {
			return false
		}
		cmd, arg, _ := strings.Cut(ans, " ")
		arg = strings.TrimSpace(arg)
		switch {
		case ans == "":
			o.f.Next()
			return true
		case ans == "b":
			o.f.Back()
			return true
		case ans == "?":
			fmt.Fprintln(o.w, "  Paths are relative to the root (or absolute inside it). Examples: a clients/acme, d 1.")
		case cmd == "a":
			if err := o.f.AddProtected(arg); err != nil {
				fmt.Fprintf(o.w, "  error: %v\n", err)
			}
		case cmd == "d":
			n, err := strconv.Atoi(arg)
			switch {
			case err == nil && n >= 1 && n <= len(prot):
				o.f.RemoveProtected(prot[n-1])
			case arg != "" && err != nil:
				o.f.RemoveProtected(arg)
			default:
				fmt.Fprintf(o.w, "  error: no entry %q\n", arg)
			}
		default:
			fmt.Fprintln(o.w, "  error: unrecognised answer; use a <path>, d <n>, b, ? or Enter")
		}
	}
}

func (o *onboarder) tools() bool {
	fmt.Fprintln(o.w, "\nStep 3 of 4: retired tools")
	root := o.f.RootDisplay()
	fmt.Fprintf(o.w, "Scanning %s for leftovers of tools you may no longer use (read-only)...\n", root)
	var dirs int64
	fps, err := o.f.Deps().Probe(o.ctx, root, o.f.Protected(), func(ev scan.Event) { dirs = ev.Dirs })
	if o.ctx.Err() != nil {
		return false
	}
	if err != nil {
		fmt.Fprintf(o.w, "Could not finish the scan: %v\n", err)
		fps = nil
	} else {
		fmt.Fprintf(o.w, "Scan done (%d directories).\n", dirs)
	}
	o.f.SetProbe(fps)
	fps = o.f.Footprints()
	for {
		if len(fps) == 0 {
			fmt.Fprintln(o.w, "No leftovers of configured retired tools were found.")
			ans, ok := o.ask("Enter to continue, b back [continue]: ")
			if !ok {
				return false
			}
			if ans == "b" {
				o.f.Back()
			} else {
				o.f.Next()
			}
			return true
		}
		fmt.Fprintln(o.w, "Tools with leftovers ([x] = mark retired, so they can be cleaned):")
		for i, fp := range fps {
			mark := " "
			if o.f.Selected(fp.Tool.ID) {
				mark = "x"
			}
			fmt.Fprintf(o.w, "  %d. [%s] %-16s %4s  %9s  youngest %s\n", i+1, mark, onboard.ToolName(fp.Tool),
				count(fp.Items, "item"), Bytes(fp.Size), onboard.FormatAge(fp.Youngest))
			if n := onboard.ToolNote(fp); n != "" {
				fmt.Fprintf(o.w, "        %s\n", n)
			}
		}
		ans, ok := o.ask("Numbers to toggle (e.g. 1 3), Enter continue [continue]: ")
		if !ok {
			return false
		}
		switch ans {
		case "":
			o.f.Next()
			return true
		case "b":
			o.f.Back()
			return true
		case "?":
			fmt.Fprintln(o.w, "  Marked tools are treated as retired: their leftovers become cleanable once old enough.")
			continue
		}
		for _, tok := range strings.FieldsFunc(ans, func(r rune) bool { return r == ' ' || r == ',' }) {
			n, err := strconv.Atoi(tok)
			if err != nil || n < 1 || n > len(fps) {
				fmt.Fprintf(o.w, "  error: no tool %q\n", tok)
				continue
			}
			o.f.ToggleTool(fps[n-1].Tool.ID)
		}
	}
}

// confirm shows the review. finished is true when the user confirmed.
func (o *onboarder) confirm() (ok, finished bool) {
	fmt.Fprintln(o.w, "\nStep 4 of 4: review")
	fmt.Fprint(o.w, o.f.Review())
	for {
		ans, ok := o.ask("Write the config file? [Y/n/b]: ")
		if !ok {
			return false, false
		}
		switch strings.ToLower(ans) {
		case "", "y", "yes":
			return true, true
		case "n", "no":
			return false, false
		case "b":
			o.f.Back()
			return true, false
		default:
			fmt.Fprintln(o.w, "  Answer y to write, n to cancel, b to go back.")
		}
	}
}
