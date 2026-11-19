package safety

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// removal matches calls that delete files or directories.
var removal = regexp.MustCompile(`\bos\.Remove(All)?\b|\.RemoveAll\(|\.Remove\(|syscall\.(Unlink|Rmdir)|unix\.(Unlink|Rmdir)`)

// TestNFR4_OnlySafetyDeletes enforces that no production code outside this package
// removes files (SPEC NFR-4, AC-E2). Test files and the fixture package are exempt.
func TestNFR4_OnlySafetyDeletes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || rel == filepath.Join("internal", "safety") || rel == filepath.Join("internal", "testfix") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if removal.MatchString(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("file removal outside internal/safety:\n%s", strings.Join(offenders, "\n"))
	}
}
