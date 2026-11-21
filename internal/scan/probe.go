package scan

import (
	"context"
	"slices"
	"time"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/safety"
)

// ToolFootprint summarises what one retired-tool candidate set looks like under the
// root (TOOL-2, TOOL-3).
type ToolFootprint struct {
	Tool      config.Tool
	Items     int // eligible + too-recent items
	TooRecent int // items younger than the tool's effective age gate
	Size      int64
	Youngest  time.Duration // age of the most recently modified counted item
}

// Probe is the read-only step-3 scan: it reports, per configured tool (whatever its
// retired value), the footprints a real run could clean, now or once old enough.
// Tools without such footprints are omitted; the result is sorted by Youngest
// ascending, then tool id.
func Probe(ctx context.Context, cfg *config.Config, root rootguard.Root, git *gitx.Runner, protected []string, emit func(Event)) ([]ToolFootprint, error) {
	return probe(ctx, cfg, root, git, protected, nil, emit)
}

func probe(ctx context.Context, cfg *config.Config, root rootguard.Root, git *gitx.Runner, protected []string, now func() time.Time, emit func(Event)) ([]ToolFootprint, error) {
	res, err := Run(ctx, cfg, root, git, Options{
		Only:      []string{config.RetiredID},
		Retired:   cfg.ToolIDs(),
		Protected: protected,
		Now:       now,
	}, emit)
	if err != nil {
		return nil, err
	}
	byID := map[string]*ToolFootprint{}
	for _, v := range res.Verdicts {
		fresh := false
		switch v.Kind {
		case safety.Eligible, safety.EligibleWithNotes:
		case safety.Skipped:
			for _, r := range v.Reasons {
				if r.Code == safety.ReasonFresh {
					fresh = true
				}
			}
			if !fresh {
				continue
			}
		default:
			continue
		}
		fp := byID[v.Candidate.Rule]
		if fp == nil {
			i := slices.IndexFunc(cfg.Retired.Tools, func(t config.Tool) bool { return t.ID == v.Candidate.Rule })
			if i < 0 {
				continue
			}
			fp = &ToolFootprint{Tool: cfg.Retired.Tools[i], Youngest: v.Age}
			byID[v.Candidate.Rule] = fp
		}
		fp.Items++
		fp.Size += v.Size
		if fresh {
			fp.TooRecent++
		}
		fp.Youngest = min(fp.Youngest, v.Age)
	}
	out := make([]ToolFootprint, 0, len(byID))
	for _, fp := range byID {
		out = append(out, *fp)
	}
	slices.SortFunc(out, func(a, b ToolFootprint) int {
		if a.Youngest != b.Youngest {
			return int(a.Youngest - b.Youngest)
		}
		return cmpStr(a.Tool.ID, b.Tool.ID)
	})
	return out, nil
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
