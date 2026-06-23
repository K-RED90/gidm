package main

import (
	"slices"
	"testing"

	"github.com/K-RED90/gidm/api"
)

func views(ids ...string) []api.DownloadView {
	out := make([]api.DownloadView, len(ids))
	for i, id := range ids {
		out[i] = api.DownloadView{ID: id}
	}
	return out
}

func TestFreshIDs(t *testing.T) {
	known := map[string]struct{}{}

	// First pass only seeds: existing downloads at launch must not surface.
	if got := freshIDs(known, false, views("a", "b")); got != nil {
		t.Fatalf("seed pass returned %v, want none", got)
	}

	// A genuinely new id after seeding surfaces; the already-known ones don't.
	if got := freshIDs(known, true, views("a", "b", "c")); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("got %v, want [c]", got)
	}

	// Nothing new → nothing surfaces.
	if got := freshIDs(known, true, views("a", "b", "c")); got != nil {
		t.Fatalf("got %v, want none", got)
	}

	// A removed-then-readded id surfaces again (it dropped out of known).
	if got := freshIDs(known, true, views("a")); got != nil {
		t.Fatalf("got %v, want none after removals", got)
	}
	if got := freshIDs(known, true, views("a", "c")); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("got %v, want [c] on re-add", got)
	}
}
