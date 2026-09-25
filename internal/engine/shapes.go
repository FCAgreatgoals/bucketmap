package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ShapeDiff is how a candidate's answer to one route differs from Discord's.
type ShapeDiff struct {
	Route string
	// Missing are fields Discord sent that the candidate did not.
	Missing []string
	// Extra are fields the candidate sent that Discord did not.
	Extra []string
	// Types are fields both sent with a different JSON type.
	Types []string
}

// CompareShapes holds a candidate's answers to Discord's, field by field,
// over every step both runs recorded with -bodies and both answered with the
// same status. A null on either side matches any type: Discord sends null
// where a value is optional. Fields are named by path, arrays as [].
func CompareShapes(direct, candidate *Report) []ShapeDiff {
	byStep := map[string]Result{}
	for _, r := range candidate.Results {
		if r.Response != nil {
			byStep[r.Step] = r
		}
	}
	byRoute := map[string]*ShapeDiff{}
	for _, d := range direct.Results {
		c, ok := byStep[d.Step]
		if !ok || d.Response == nil || c.Status != d.Status {
			continue
		}
		var dv, cv any
		if json.Unmarshal(d.Response, &dv) != nil || json.Unmarshal(c.Response, &cv) != nil {
			continue
		}
		route := d.Method + " " + d.Route
		diff := byRoute[route]
		if diff == nil {
			diff = &ShapeDiff{Route: route}
			byRoute[route] = diff
		}
		compareShape("", dv, cv, diff)
	}

	var out []ShapeDiff
	for _, d := range byRoute {
		d.Missing, d.Extra, d.Types = dedupe(d.Missing), dedupe(d.Extra), dedupe(d.Types)
		if len(d.Missing)+len(d.Extra)+len(d.Types) > 0 {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

func compareShape(path string, d, c any, diff *ShapeDiff) {
	if d == nil || c == nil {
		return
	}
	switch dt := d.(type) {
	case map[string]any:
		ct, ok := c.(map[string]any)
		if !ok {
			diff.Types = append(diff.Types, fmt.Sprintf("%s: %s, candidate %s", field(path), kind(d), kind(c)))
			return
		}
		for k, dv := range dt {
			cv, ok := ct[k]
			if !ok {
				diff.Missing = append(diff.Missing, join(path, k))
				continue
			}
			compareShape(join(path, k), dv, cv, diff)
		}
		for k := range ct {
			if _, ok := dt[k]; !ok {
				diff.Extra = append(diff.Extra, join(path, k))
			}
		}
	case []any:
		ct, ok := c.([]any)
		if !ok {
			diff.Types = append(diff.Types, fmt.Sprintf("%s: %s, candidate %s", field(path), kind(d), kind(c)))
			return
		}
		// Elements are compared by shape, first with first: lists hold
		// objects of one kind, and their order or length is data, not shape.
		if len(dt) > 0 && len(ct) > 0 {
			compareShape(path+"[]", dt[0], ct[0], diff)
		}
	default:
		if kind(d) != kind(c) {
			diff.Types = append(diff.Types, fmt.Sprintf("%s: %s, candidate %s", field(path), kind(d), kind(c)))
		}
	}
}

func kind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "null"
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func field(path string) string {
	if path == "" {
		return "(the answer)"
	}
	return path
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// PrintShapes writes the differences, route by route.
func PrintShapes(w io.Writer, diffs []ShapeDiff) {
	if len(diffs) == 0 {
		fmt.Fprintln(w, "\nsame fields and types on every recorded answer")
		return
	}
	fmt.Fprintf(w, "\nanswers shaped differently, %d routes\n", len(diffs))
	for _, d := range diffs {
		fmt.Fprintf(w, "  %s\n", d.Route)
		for _, list := range []struct {
			label string
			items []string
		}{{"missing", d.Missing}, {"extra", d.Extra}, {"type", d.Types}} {
			if len(list.items) > 0 {
				fmt.Fprintf(w, "    %-8s %s\n", list.label, strings.Join(list.items, ", "))
			}
		}
	}
}
