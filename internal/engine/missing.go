package engine

import (
	"sort"
	"time"
)

// Missing lists the groups worth running again after the given reports: those
// with a route no report has seen answer, or seen only on a bucket whose model
// is still unknown. Routes Discord does not limit on their own count as known.
func Missing(reports ...*Report) []string {
	known := map[string]bool{}
	merged := Merge(reports...)
	for _, b := range merged.Buckets {
		if b.Model == "unknown" {
			continue
		}
		for _, route := range b.Routes {
			known[route] = true
		}
	}

	var out []string
	seen := map[string]bool{}
	for _, res := range DryRun(true).Results {
		name := res.Method + " " + res.Route
		if res.Group == "" || res.Group == "preflight" || res.Group == "setup" || res.Group == "cleanup" {
			continue
		}
		if !known[name] && !seen[res.Group] {
			seen[res.Group] = true
			out = append(out, res.Group)
		}
	}
	// Keep the scenario's order.
	order := map[string]int{}
	for i, g := range Groups() {
		order[g] = i
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out
}

// Merge combines the reports of several runs against the same API into one:
// every request, and buckets classified again over all of them. A later run
// can settle a model an earlier one left unknown.
func Merge(reports ...*Report) *Report {
	out := &Report{}
	for _, r := range reports {
		if r == nil {
			continue
		}
		if out.API == "" {
			out.API, out.Spacing = r.API, r.Spacing
		}
		if out.Started.IsZero() || r.Started.Before(out.Started) {
			out.Started = r.Started
		}
		if r.Finished.After(out.Finished) {
			out.Finished = r.Finished
		}
		out.Results = append(out.Results, r.Results...)
		out.Failures = append(out.Failures, r.Failures...)
		out.Skipped = append(out.Skipped, r.Skipped...)
		if r.Invite != "" {
			out.Invite = r.Invite
		}
	}
	sort.SliceStable(out.Results, func(i, j int) bool { return out.Results[i].At.Before(out.Results[j].At) })
	out.Buckets = modelBuckets(out.Results)
	if out.Finished.IsZero() {
		out.Finished = time.Now()
	}
	return out
}
