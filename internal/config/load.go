package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jfricker/cleanup/internal/rootguard"
)

//go:embed defaults.toml
var defaultsTOML []byte

// SupportedVersion is the only accepted config schema version.
const SupportedVersion = 1

// Defaults returns the embedded default configuration file.
func Defaults() []byte { return slices.Clone(defaultsTOML) }

// LoadError is one problem in a config file.
type LoadError struct {
	File string
	Line int    // 0 when unknown
	Key  string // dotted key path
	Msg  string
	Hint string // e.g. a suggested key
}

func (e *LoadError) Error() string {
	var b strings.Builder
	b.WriteString(e.File)
	if e.Line > 0 {
		fmt.Fprintf(&b, ":%d", e.Line)
	}
	if e.Key != "" {
		b.WriteString(": " + e.Key)
	}
	b.WriteString(": " + e.Msg)
	if e.Hint != "" {
		b.WriteString(" (" + e.Hint + ")")
	}
	return b.String()
}

// Load returns the defaults merged with the user file at userPath. An empty
// userPath loads the defaults alone.
func Load(userPath string) (*Config, error) {
	if userPath == "" {
		return LoadBytes("", nil)
	}
	data, err := os.ReadFile(userPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return LoadBytes(userPath, data)
}

// LoadBytes merges user (named name in errors) over the defaults. A nil user loads
// the defaults alone.
func LoadBytes(name string, user []byte) (*Config, error) {
	base, err := decode("defaults.toml", defaultsTOML)
	if err != nil {
		return nil, fmt.Errorf("embedded defaults are invalid: %w", err)
	}
	merged := base
	text := string(defaultsTOML)
	if user != nil {
		over, err := decode(name, user)
		if err != nil {
			return nil, err
		}
		merged = merge(base, over)
		text = string(user)
	}
	cfg, errs := validate(merged, name, indexText(text), user != nil)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

func decode(name string, data []byte) (*rawFile, error) {
	var rf rawFile
	md, err := toml.Decode(string(data), &rf)
	if err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			return nil, &LoadError{File: name, Line: pe.Position.Line, Msg: pe.Message}
		}
		return nil, &LoadError{File: name, Msg: err.Error()}
	}
	if rf.Version == nil {
		return nil, &LoadError{File: name, Key: "version",
			Msg:  fmt.Sprintf("missing `version = %d`", SupportedVersion),
			Hint: "cleanup v1 requires a versioned config; prototype configs must be updated, compare with `cleanup --print-config`"}
	}
	blocks := indexText(string(data))
	if *rf.Version != SupportedVersion {
		return nil, &LoadError{File: name, Line: locate(blocks, nil, "version", ""), Key: "version",
			Msg: fmt.Sprintf("unsupported version %d, want %d", *rf.Version, SupportedVersion)}
	}
	var errs []error
	reported := map[string]bool{}
	for _, k := range md.Undecoded() {
		valid, bad := validKeys(k)
		if bad >= len(k) {
			continue // a child of an already-reported unknown table
		}
		prefix := strings.Join(k[:bad+1], ".")
		if reported[prefix] {
			continue
		}
		reported[prefix] = true
		le := &LoadError{File: name, Key: prefix, Msg: "unknown key"}
		if s := suggest(k[bad], valid); s != "" {
			le.Hint = "did you mean " + s + "?"
		}
		le.Line = locate(blocks, k[:bad], k[bad], "")
		if le.Line == 0 {
			le.Line = locate(blocks, k[:bad+1], "", "")
		}
		errs = append(errs, le)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &rf, nil
}

// merge overlays u onto b (CFG-1): tables merge by key or id, set scalars and arrays
// replace, and a category's rules are replaced only when the user sets them.
func merge(b, u *rawFile) *rawFile {
	out := *b
	setIf(&out.ProjectRoot, u.ProjectRoot)
	setIf(&out.ProtectedPaths, u.ProtectedPaths)
	if u.Global != nil && u.Global.NeverTouch != nil {
		out.Global = &rawGlobal{NeverTouch: u.Global.NeverTouch}
	}
	if u.Providers != nil && u.Providers.Worktrees != nil {
		w := *orEmpty(b.Providers).Worktrees
		mergeProvider(&w, u.Providers.Worktrees)
		out.Providers = &rawProviders{Worktrees: &w}
	}
	if len(u.Categories) > 0 {
		out.Categories = make(map[string]*rawCategory, len(b.Categories)+len(u.Categories))
		for k, v := range b.Categories {
			c := *v
			out.Categories[k] = &c
		}
		for k, uc := range u.Categories {
			c, ok := out.Categories[k]
			if !ok {
				c = &rawCategory{}
				out.Categories[k] = c
			}
			setIf(&c.Enabled, uc.Enabled)
			setIf(&c.Label, uc.Label)
			setIf(&c.Description, uc.Description)
			setIf(&c.SortOrder, uc.SortOrder)
			setIf(&c.Prune, uc.Prune)
			setIf(&c.Rules, uc.Rules)
		}
	}
	if u.Retired != nil {
		r := *b.Retired
		setIf(&r.Enabled, u.Retired.Enabled)
		setIf(&r.Label, u.Retired.Label)
		setIf(&r.Description, u.Retired.Description)
		setIf(&r.AgeDays, u.Retired.AgeDays)
		setIf(&r.SortOrder, u.Retired.SortOrder)
		r.Tools = slices.Clone(b.Retired.Tools)
		for _, ut := range u.Retired.Tools {
			i := slices.IndexFunc(r.Tools, func(t rawTool) bool { return t.ID == ut.ID })
			if i < 0 {
				r.Tools = append(r.Tools, ut)
				continue
			}
			t := &r.Tools[i]
			setIf(&t.Name, ut.Name)
			setIf(&t.Retired, ut.Retired)
			setIf(&t.AgeDays, ut.AgeDays)
			setIf(&t.Dirs, ut.Dirs)
			setIf(&t.Files, ut.Files)
			setIf(&t.Note, ut.Note)
		}
		out.Retired = &r
	}
	return &out
}

func mergeProvider(dst, src *rawProvider) {
	setIf(&dst.Enabled, src.Enabled)
	setIf(&dst.Label, src.Label)
	setIf(&dst.Description, src.Description)
	setIf(&dst.AgeDays, src.AgeDays)
	setIf(&dst.SortOrder, src.SortOrder)
}

func orEmpty(p *rawProviders) *rawProviders {
	if p == nil || p.Worktrees == nil {
		return &rawProviders{Worktrees: &rawProvider{}}
	}
	return p
}

func setIf[T any](dst **T, src *T) {
	if src != nil {
		*dst = src
	}
}

func val[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

type validator struct {
	file   string
	blocks []block
	errs   []error
}

func (v *validator) fail(path []string, key, id, format string, args ...any) {
	line := locate(v.blocks, path, key, id)
	k := strings.Join(append(slices.Clone(path), key), ".")
	if id != "" {
		k = strings.Join(path, ".") + "[id=" + id + "]." + key
	}
	v.errs = append(v.errs, &LoadError{File: v.file, Line: line, Key: k, Msg: fmt.Sprintf(format, args...)})
}

func validate(rf *rawFile, name string, blocks []block, isUser bool) (*Config, []error) {
	file := name
	if !isUser {
		file = "defaults.toml"
	}
	v := &validator{file: file, blocks: blocks}
	cfg := &Config{}

	v.projectRoot(cfg, rf.ProjectRoot)
	v.protectedPaths(cfg, rf.ProtectedPaths)

	cfg.NeverTouch = slices.Clone(val(val(rf.Global).NeverTouch))
	for _, n := range cfg.NeverTouch {
		if err := checkName(n, false); err != nil {
			v.fail([]string{"global"}, "never_touch", "", "%q: %v", n, err)
		}
	}
	if !slices.Contains(cfg.NeverTouch, ".git") { // CFG-6
		cfg.NeverTouch = append([]string{".git"}, cfg.NeverTouch...)
	}

	w := val(val(rf.Providers).Worktrees)
	cfg.Worktrees = Provider{Enabled: val(w.Enabled), Label: val(w.Label), Description: val(w.Description),
		AgeDays: val(w.AgeDays), SortOrder: val(w.SortOrder)}
	if cfg.Worktrees.AgeDays < 0 {
		v.fail([]string{"providers", "worktrees"}, "age_days", "", "must be >= 0")
	}

	for id, rc := range rf.Categories {
		cfg.Categories = append(cfg.Categories, v.category(id, rc))
	}
	slices.SortFunc(cfg.Categories, func(a, b Category) int {
		if a.SortOrder != b.SortOrder {
			return a.SortOrder - b.SortOrder
		}
		return strings.Compare(a.ID, b.ID)
	})

	rt := val(rf.Retired)
	cfg.Retired = RetiredTools{Enabled: val(rt.Enabled), Label: val(rt.Label), Description: val(rt.Description),
		SortOrder: val(rt.SortOrder)}
	defAge := 30
	if rt.AgeDays != nil {
		defAge = *rt.AgeDays
		if defAge < 1 {
			v.fail([]string{"retired_tools"}, "age_days", "", "must be >= 1 (retired tools are always age-gated)")
		}
	}
	seen := map[string]bool{}
	for _, t := range rt.Tools {
		path := []string{"retired_tools", "tools"}
		if t.ID == "" {
			v.fail(path, "id", "", "tool id is required")
			continue
		}
		if seen[t.ID] {
			v.fail(path, "id", t.ID, "duplicate tool id %q", t.ID)
		}
		seen[t.ID] = true
		tool := Tool{ID: t.ID, Name: val(t.Name), Retired: val(t.Retired), AgeDays: defAge,
			Dirs: val(t.Dirs), Files: val(t.Files), Note: val(t.Note)}
		if tool.Name == "" {
			tool.Name = t.ID
		}
		if t.AgeDays != nil {
			tool.AgeDays = *t.AgeDays
			if tool.AgeDays < 1 {
				v.fail(path, "age_days", t.ID, "must be >= 1 (retired tools are always age-gated)")
			}
		}
		for _, p := range append(slices.Clone(tool.Dirs), tool.Files...) {
			if err := checkPattern(p); err != nil {
				v.fail(path, "dirs", t.ID, "%q: %v", p, err)
			}
		}
		cfg.Retired.Tools = append(cfg.Retired.Tools, tool)
	}
	return cfg, v.errs
}

// projectRoot validates project_root (PROOT-4): non-empty and absolute after tilde
// expansion. Existence and rootguard checks are run-time, not load-time.
func (v *validator) projectRoot(cfg *Config, p *string) {
	if p == nil {
		return
	}
	raw := *p
	if raw == "" {
		v.fail(nil, "project_root", "", "must not be empty")
		return
	}
	exp := raw
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := rootguard.Home()
		if err != nil {
			v.fail(nil, "project_root", "", "%q: %v", raw, err)
			return
		}
		exp = rootguard.Expand(raw, home)
	}
	if !path.IsAbs(exp) {
		v.fail(nil, "project_root", "", "%q must be an absolute path (or start with ~/)", raw)
		return
	}
	cfg.ProjectRoot = exp
}

// protectedPaths validates protected_paths (PROT-3) and collapses duplicates and
// nested entries to the outermost.
func (v *validator) protectedPaths(cfg *Config, ps *[]string) {
	if ps == nil {
		return
	}
	var clean []string
	for _, e := range *ps {
		c, err := checkProtected(e)
		if err != nil {
			v.fail(nil, "protected_paths", "", "%q: %v", e, err)
			continue
		}
		clean = append(clean, c)
	}
	slices.Sort(clean)
	clean = slices.Compact(clean)
	var out []string
	for _, c := range clean {
		if len(out) > 0 && strings.HasPrefix(c, out[len(out)-1]+"/") {
			continue // nested under an earlier (outer) entry
		}
		out = append(out, c)
	}
	cfg.ProtectedPaths = out
}

func checkProtected(e string) (string, error) {
	switch {
	case e == "":
		return "", errors.New("must not be empty")
	case strings.Contains(e, `\`):
		return "", errors.New("must use / separators, not backslash")
	case path.IsAbs(e):
		return "", errors.New("must be relative to the project root, not absolute")
	}
	c := path.Clean(e)
	if c == "." {
		return "", errors.New("must name a path below the project root, not the root itself")
	}
	if slices.Contains(strings.Split(c, "/"), "..") {
		return "", errors.New("must not escape the project root (no .. components)")
	}
	return c, nil
}

func (v *validator) category(id string, rc *rawCategory) Category {
	path := []string{"categories", id}
	c := Category{ID: id, Enabled: val(rc.Enabled), Label: val(rc.Label), Description: val(rc.Description),
		SortOrder: val(rc.SortOrder), Prune: val(rc.Prune)}
	if id == WorktreesID || id == RetiredID {
		v.fail(path, "", "", "category id %q is reserved", id)
	}
	if c.Label == "" {
		c.Label = id
	}
	for _, p := range c.Prune {
		if err := checkName(p, false); err != nil {
			v.fail(path, "prune", "", "%q: %v", p, err)
		}
	}
	rpath := append(slices.Clone(path), "rules")
	seen := map[string]bool{}
	for _, r := range val(rc.Rules) {
		if r.ID == "" {
			v.fail(rpath, "id", "", "rule id is required")
			continue
		}
		if seen[r.ID] {
			v.fail(rpath, "id", r.ID, "duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		rule := Rule{ID: r.ID, Category: r.Category, Badge: r.Badge, Names: r.Names, Globs: r.Globs,
			SelfMarkers: r.RequireSelfMarkers, SiblingMarkers: r.RequireSiblingMarkers,
			AncestorMarkers: r.RequireAncestorMarkers, Warning: r.Warning}
		var err error
		if rule.Kind, err = parseKind(r.Kind); err != nil {
			v.fail(rpath, "kind", r.ID, "%v", err)
		}
		if rule.Strategy, err = parseStrategy(r.Strategy); err != nil {
			v.fail(rpath, "strategy", r.ID, "%v", err)
		}
		if rule.OptIn, err = parseOptIn(r.OptIn); err != nil {
			v.fail(rpath, "opt_in", r.ID, "%v", err)
		}
		if rule.Kind == KindFile && rule.Strategy == StrategyEmptyContents {
			v.fail(rpath, "strategy", r.ID, "empty-contents applies only to kind = \"dir\"")
		}
		if len(r.Names)+len(r.Globs) == 0 {
			v.fail(rpath, "names", r.ID, "a rule needs at least one name or glob")
		}
		for _, n := range r.Names {
			if err := checkName(n, true); err != nil {
				v.fail(rpath, "names", r.ID, "%q: %v", n, err)
			}
		}
		for _, g := range r.Globs {
			if err := checkPattern(g); err != nil {
				v.fail(rpath, "globs", r.ID, "%q: %v", g, err)
			}
		}
		for _, m := range slices.Concat(r.RequireSelfMarkers, r.RequireSiblingMarkers, r.RequireAncestorMarkers) {
			if err := checkMarker(m); err != nil {
				v.fail(rpath, "require_self_markers", r.ID, "%q: %v", m, err)
			}
		}
		c.Rules = append(c.Rules, rule)
	}
	if c.Enabled && len(c.Rules) == 0 {
		v.fail(path, "rules", "", "an enabled category needs at least one rule")
	}
	return c
}

// checkName validates a literal entry name (CFG-5).
func checkName(n string, forbidGit bool) error {
	switch {
	case n == "", n == ".", n == "..":
		return errors.New("not a valid name")
	case strings.ContainsAny(n, "/\x00"):
		return errors.New("must be a single path component")
	case forbidGit && n == ".git":
		return errors.New("must not match .git")
	case forbidGit && strings.Trim(n, "*?.") == "":
		return errors.New("too broad: matches almost everything")
	}
	return nil
}

// checkPattern validates a glob or name pattern (CFG-5).
func checkPattern(p string) error {
	if err := checkName(p, true); err != nil {
		return err
	}
	if _, err := path.Match(p, ""); err != nil {
		return fmt.Errorf("invalid glob: %w", err)
	}
	if strings.Trim(p, "*?.") == "" {
		return errors.New("too broad: matches almost everything")
	}
	if m, _ := path.Match(p, ".git"); m {
		return errors.New("must not match .git")
	}
	return nil
}

// checkMarker validates a marker path, which may have several components.
func checkMarker(m string) error {
	if m == "" || strings.HasPrefix(m, "/") {
		return errors.New("must be a relative path")
	}
	if slices.Contains(strings.Split(m, "/"), "..") {
		return errors.New("must not contain a .. component")
	}
	return nil
}
