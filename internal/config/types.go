// Package config loads the embedded default configuration, merges a user file over
// it and validates the result strictly. Every enumerated value decodes into a typed
// constant whose zero value is invalid, so a missed conversion can never fall back to
// deleting a whole directory.
package config

import "fmt"

// Kind is what a rule matches.
type Kind uint8

// Rule kinds. The zero value is invalid.
const (
	KindInvalid Kind = iota
	KindDir
	KindFile
)

func (k Kind) String() string {
	switch k {
	case KindDir:
		return "dir"
	case KindFile:
		return "file"
	}
	return "invalid"
}

func parseKind(s string) (Kind, error) {
	switch s {
	case "dir":
		return KindDir, nil
	case "file":
		return KindFile, nil
	}
	return KindInvalid, fmt.Errorf("must be \"dir\" or \"file\", got %q", s)
}

// Strategy is how a matched directory is cleaned.
type Strategy uint8

// Strategies. The zero value is invalid.
const (
	StrategyInvalid Strategy = iota
	StrategyRemove
	StrategyEmptyContents
)

func (s Strategy) String() string {
	switch s {
	case StrategyRemove:
		return "remove"
	case StrategyEmptyContents:
		return "empty-contents"
	}
	return "invalid"
}

func parseStrategy(s string) (Strategy, error) {
	switch s {
	case "", "remove":
		return StrategyRemove, nil
	case "empty-contents":
		return StrategyEmptyContents, nil
	}
	return StrategyInvalid, fmt.Errorf("must be \"remove\" or \"empty-contents\", got %q", s)
}

// OptIn names a CLI flag a rule requires before it matches.
type OptIn uint8

// Opt-ins. The zero value is invalid; OptInNone means the rule always applies.
const (
	OptInInvalid OptIn = iota
	OptInNone
	OptInVenvs
	OptInModules
)

func (o OptIn) String() string {
	switch o {
	case OptInNone:
		return ""
	case OptInVenvs:
		return "venvs"
	case OptInModules:
		return "modules"
	}
	return "invalid"
}

func parseOptIn(s string) (OptIn, error) {
	switch s {
	case "":
		return OptInNone, nil
	case "venvs":
		return OptInVenvs, nil
	case "modules":
		return OptInModules, nil
	}
	return OptInInvalid, fmt.Errorf("must be \"venvs\" or \"modules\", got %q", s)
}

// Reserved cleaner IDs for the non-category cleaners.
const (
	WorktreesID = "worktrees"
	RetiredID   = "retired"
)

// Config is the validated, merged configuration.
type Config struct {
	NeverTouch []string
	Worktrees  Provider
	Categories []Category // sorted by SortOrder, then ID
	Retired    RetiredTools
}

// Provider configures the git worktrees provider.
type Provider struct {
	Enabled     bool
	Label       string
	Description string
	AgeDays     int
	SortOrder   int
}

// Category is a declarative artifact cleaner.
type Category struct {
	ID          string
	Enabled     bool
	Label       string
	Description string
	SortOrder   int
	Prune       []string
	Rules       []Rule
}

// Rule matches candidate paths within a category.
type Rule struct {
	ID              string
	Category        string
	Badge           string
	Kind            Kind
	Names           []string
	Globs           []string
	OptIn           OptIn
	SelfMarkers     []string
	SiblingMarkers  []string
	AncestorMarkers []string
	Strategy        Strategy
	Warning         string
}

// RetiredTools configures the retired-tools cleaner.
type RetiredTools struct {
	Enabled     bool
	Label       string
	Description string
	SortOrder   int
	Tools       []Tool
}

// Tool is one retired-tool definition.
type Tool struct {
	ID      string
	Name    string
	Retired bool
	AgeDays int
	Dirs    []string
	Files   []string
	Note    string
}

// CleanerIDs returns every cleaner ID usable with --only and --exclude.
func (c *Config) CleanerIDs() []string {
	ids := make([]string, 0, len(c.Categories)+2)
	for _, cat := range c.Categories {
		ids = append(ids, cat.ID)
	}
	return append(ids, WorktreesID, RetiredID)
}

// ToolIDs returns every configured retired-tool ID (usable with --retired).
func (c *Config) ToolIDs() []string {
	ids := make([]string, len(c.Retired.Tools))
	for i, t := range c.Retired.Tools {
		ids[i] = t.ID
	}
	return ids
}
