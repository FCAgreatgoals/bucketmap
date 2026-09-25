package engine

import (
	"slices"
	"testing"
	"time"
)

func TestOnlyPullsInTheGroupsItNeeds(t *testing.T) {
	got, err := selected([]string{"reactions"})
	if err != nil {
		t.Fatal(err)
	}
	if !got["reactions"] || !got["messages"] || got["webhooks"] {
		t.Fatalf("selected = %v", got)
	}
	if _, err := selected([]string{"nope"}); err == nil {
		t.Fatal("an unknown group was accepted")
	}
}

// A dry run restricted to one group only calls that group's routes, besides
// the preflight, the setup and the cleanup.
func TestDryRunOnlyRunsTheSelectedGroups(t *testing.T) {
	for _, res := range dryRun(true, []string{"emojis"}).Results {
		switch res.Group {
		case "preflight", "setup", "cleanup", "emojis":
		default:
			t.Fatalf("%s ran in group %q", res.Step, res.Group)
		}
	}
}

// With nothing known, every group is missing; once a group's routes all have
// a model, it no longer is.
func TestMissingListsGroupsWithUnknownRoutes(t *testing.T) {
	if got := Missing(); len(got) != len(groups) {
		t.Fatalf("with no report, %d groups missing, want %d", len(got), len(groups))
	}
	var known []Result
	at := time.Unix(1_800_000_000, 0)
	for _, res := range DryRun(true).Results {
		if res.Group != "emojis" {
			continue
		}
		known = append(known, Result{Method: res.Method, Route: res.Route, Bucket: res.Method + res.Route, Limit: 1000, Remaining: 999, ResetAfter: 0.001, At: at})
		at = at.Add(time.Second)
	}
	got := Missing(&Report{Results: known})
	if slices.Contains(got, "emojis") {
		t.Fatalf("emojis still missing once all its routes are known: %v", got)
	}
}

func TestMergeKeepsEveryRequestInOrder(t *testing.T) {
	a := &Report{API: "x", Results: []Result{{Step: "b", At: time.Unix(2, 0)}}}
	b := &Report{API: "x", Results: []Result{{Step: "a", At: time.Unix(1, 0)}}}
	got := Merge(a, b)
	if len(got.Results) != 2 || got.Results[0].Step != "a" {
		t.Fatalf("merged = %+v", got.Results)
	}
}
