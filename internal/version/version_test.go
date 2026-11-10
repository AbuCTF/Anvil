package version

import "testing"

func TestResolveCommitPrefersStamp(t *testing.T) {
	saved := Commit
	defer func() { Commit = saved }()

	Commit = "c03ff39"
	if got := resolveCommit(); got != "c03ff39" {
		t.Fatalf("resolveCommit() = %q, want stamped commit", got)
	}
}

func TestInfoAlwaysReportsSomething(t *testing.T) {
	v, c := Info()
	if v == "" || c == "" {
		t.Fatalf("Info() = (%q, %q), want non-empty version and commit", v, c)
	}
}
