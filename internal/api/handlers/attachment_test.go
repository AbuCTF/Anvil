package handlers

import "testing"

func TestSanitiseFilenameStripsPathsAndHeaderDelimiters(t *testing.T) {
	for name, want := range map[string]string{
		"../../payload.ova":  "payload.ova",
		`..\..\payload.ova`:  "payload.ova",
		"report\"; bad=.txt": "report; bad=.txt",
		"\x00\x01":           "file",
	} {
		if got := sanitiseFilename(name); got != want {
			t.Errorf("sanitiseFilename(%q) = %q, want %q", name, got, want)
		}
	}
}
