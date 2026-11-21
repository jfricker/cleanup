package config

// Raw types mirror the TOML file. Pointers distinguish "unset" from zero so a user
// file can override individual fields of the defaults.

type rawFile struct {
	Version        *int                    `toml:"version"`
	ProjectRoot    *string                 `toml:"project_root"`
	ProtectedPaths *[]string               `toml:"protected_paths"`
	Global         *rawGlobal              `toml:"global"`
	Providers      *rawProviders           `toml:"providers"`
	Categories     map[string]*rawCategory `toml:"categories"`
	Retired        *rawRetired             `toml:"retired_tools"`
}

type rawGlobal struct {
	NeverTouch *[]string `toml:"never_touch"`
}

type rawProviders struct {
	Worktrees *rawProvider `toml:"worktrees"`
}

type rawProvider struct {
	Enabled     *bool   `toml:"enabled"`
	Label       *string `toml:"label"`
	Description *string `toml:"description"`
	AgeDays     *int    `toml:"age_days"`
	SortOrder   *int    `toml:"sort_order"`
}

type rawCategory struct {
	Enabled     *bool      `toml:"enabled"`
	Label       *string    `toml:"label"`
	Description *string    `toml:"description"`
	SortOrder   *int       `toml:"sort_order"`
	Prune       *[]string  `toml:"prune"`
	Rules       *[]rawRule `toml:"rules"`
}

// rawRule is replaced wholesale when a user sets a category's rules, so its fields
// need no pointers.
type rawRule struct {
	ID                     string   `toml:"id,omitempty"`
	Category               string   `toml:"category,omitempty"`
	Badge                  string   `toml:"badge,omitempty"`
	Kind                   string   `toml:"kind,omitempty"`
	Names                  []string `toml:"names,omitempty"`
	Globs                  []string `toml:"globs,omitempty"`
	OptIn                  string   `toml:"opt_in,omitempty"`
	RequireSelfMarkers     []string `toml:"require_self_markers,omitempty"`
	RequireSiblingMarkers  []string `toml:"require_sibling_markers,omitempty"`
	RequireAncestorMarkers []string `toml:"require_ancestor_markers,omitempty"`
	Strategy               string   `toml:"strategy,omitempty"`
	Warning                string   `toml:"warning,omitempty"`
}

type rawRetired struct {
	Enabled     *bool     `toml:"enabled"`
	Label       *string   `toml:"label"`
	Description *string   `toml:"description"`
	AgeDays     *int      `toml:"age_days"`
	SortOrder   *int      `toml:"sort_order"`
	Tools       []rawTool `toml:"tools,omitempty"`
}

type rawTool struct {
	ID      string    `toml:"id"`
	Name    *string   `toml:"name"`
	Retired *bool     `toml:"retired"`
	AgeDays *int      `toml:"age_days"`
	Dirs    *[]string `toml:"dirs"`
	Files   *[]string `toml:"files"`
	Note    *string   `toml:"note"`
}
