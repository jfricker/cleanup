package config

import (
	"reflect"
	"regexp"
	"strings"
)

// BurntSushi/toml reports key paths but not line numbers, so error positions are
// recovered by re-scanning the file text. This is a best-effort index: when a key
// can't be found, errors are reported without a line.

type block struct {
	header string         // dotted table path, "" for the root table
	line   int            // 1-based line of the header (0 for root)
	id     string         // value of an `id = "..."` key in the block, if any
	keys   map[string]int // first-level key → line
}

var (
	headerRe = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
	keyRe    = regexp.MustCompile(`^\s*("?[A-Za-z0-9_\-]+"?)\s*=`)
	idRe     = regexp.MustCompile(`^\s*id\s*=\s*"([^"]*)"`)
)

func indexText(text string) []block {
	blocks := []block{{keys: map[string]int{}}}
	for i, ln := range strings.Split(text, "\n") {
		n := i + 1
		if m := headerRe.FindStringSubmatch(ln); m != nil {
			blocks = append(blocks, block{header: normHeader(m[1]), line: n, keys: map[string]int{}})
			continue
		}
		cur := &blocks[len(blocks)-1]
		if m := idRe.FindStringSubmatch(ln); m != nil && cur.id == "" {
			cur.id = m[1]
		}
		if m := keyRe.FindStringSubmatch(ln); m != nil {
			k := strings.Trim(m[1], `"`)
			if _, ok := cur.keys[k]; !ok {
				cur.keys[k] = n
			}
		}
	}
	return blocks
}

func normHeader(h string) string {
	parts := strings.Split(h, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"'`)
	}
	return strings.Join(parts, ".")
}

// locate returns the line of key inside the table at path (optionally the
// array-of-tables entry whose id is id). With key == "" it returns the header line.
// It returns 0 when nothing matches.
func locate(blocks []block, path []string, key, id string) int {
	header := strings.Join(path, ".")
	fallback := 0
	for _, b := range blocks {
		if b.header != header || (id != "" && b.id != id) {
			continue
		}
		if key == "" {
			return b.line
		}
		if n, ok := b.keys[key]; ok {
			return n
		}
		if fallback == 0 {
			fallback = b.line
		}
	}
	return fallback
}

// validKeys walks the raw schema along path and returns the keys allowed in the
// table where path stops matching, plus the index of the first unknown component.
func validKeys(path []string) ([]string, int) {
	t := reflect.TypeOf(rawFile{})
	for i := 0; i < len(path); i++ {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Map:
			t = t.Elem() // the map key consumes this component
			continue
		case reflect.Struct:
			f, ok := fieldByTag(t, path[i])
			if !ok {
				return tags(t), i
			}
			t = f.Type
		default:
			return nil, i
		}
	}
	return nil, len(path)
}

func fieldByTag(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if strings.Split(f.Tag.Get("toml"), ",")[0] == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func tags(t reflect.Type) []string {
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		out = append(out, strings.Split(t.Field(i).Tag.Get("toml"), ",")[0])
	}
	return out
}

// suggest returns the closest candidate to s within a small edit distance.
func suggest(s string, candidates []string) string {
	best, bestD := "", len(s)/2+2
	for _, c := range candidates {
		if d := levenshtein(s, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
