package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jfricker/cleanup/internal/cli"
	"github.com/jfricker/cleanup/internal/execute"
	"github.com/jfricker/cleanup/internal/safety"
	"github.com/jfricker/cleanup/internal/scan"
	"github.com/jfricker/cleanup/internal/tui"
)

func init() { runTUI = runInteractive }

func runInteractive(s *session) int {
	var res *scan.Result
	group := tui.ByCleaner
	if s.opts.GroupBy == cli.GroupProject {
		group = tui.ByProject
	}
	code, err := tui.Run(tui.Deps{
		Options: tui.Options{
			Root: s.root.Path, Delete: s.opts.Mode == cli.Delete, ShowSkipped: s.opts.ShowSkipped,
			GroupBy: group, Cleaners: s.cleaners,
		},
		Scan: func(ctx context.Context, emit func(scan.Event)) (*scan.Result, error) {
			r, err := scan.Run(ctx, s.cfg, s.root, s.git, s.scanOpts, emit)
			res = r
			return r, err
		},
		Execute: func(sel []safety.Verdict, emit func(execute.Event)) (*execute.Report, string, int, error) {
			rep, logPath, code := s.execute(res, sel, emit)
			if rep == nil {
				return nil, "", code, errors.New("cannot start deletion; nothing was deleted")
			}
			return rep, logPath, code, nil
		},
		SoftCancel: s.signals.SoftCancel,
		HardCancel: s.signals.HardCancel,
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "cleanup: %v\n", err)
		return exitUsage
	}
	return code
}
