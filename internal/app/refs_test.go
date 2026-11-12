package app

import (
	"slices"
	"testing"
)

func TestArtifactRefsFollowTheDesksRules(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"Steps: artifact:frostread-v060-release.", []string{"frostread-v060-release"}},
		{"see (artifact:a-b) and \"artifact:c\"", []string{"a-b", "c"}},
		{"code `artifact:in-code` counts whole", []string{"in-code"}},
		{"`run artifact:not-whole` does not", nil},
		{"no boundary xartifact:nope", nil},
		{"bad id artifact:Bad_ID and artifact:-x", nil},
		{"twice artifact:same and artifact:same", []string{"same"}},
		{"```\nartifact:fenced\n```\nafter artifact:after", []string{"after"}},
	} {
		if got := refIDs(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("%q: %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestLinkArtifactsMakesLinks(t *testing.T) {
	got := linkArtifacts("Steps: artifact:rel-notes, and `artifact:x`.", "12")
	want := "Steps: [artifact:rel-notes](artifact:rel-notes#12), and [`artifact:x`](artifact:x#12)."
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	// A reference that is already a link's destination stays as it is.
	linked := "[the notes](artifact:rel-notes)"
	if got := linkArtifacts(linked, ""); got != linked {
		t.Errorf("linked: %q", got)
	}
}
