package cli

import "testing"

func TestGroupByDefaultsToProject(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want GroupBy
	}{
		{nil, GroupProject},
		{[]string{"-g", "cleaner"}, GroupCleaner},
		{[]string{"--group-by", "project"}, GroupProject},
	} {
		o, err := Parse(tc.args)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.args, err)
		}
		if o.GroupBy != tc.want {
			t.Errorf("Parse(%q).GroupBy = %v, want %v", tc.args, o.GroupBy, tc.want)
		}
	}
}
