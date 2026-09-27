package indexgen

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/FCAgreatgoals/bucketmap/internal/engine"
	"github.com/FCAgreatgoals/bucketmap/routes"
)

// models maps the report's names to the index's.
var models = map[string]routes.Model{
	"token bucket": routes.TokenBucket,
	"fixed window": routes.FixedWindow,
	"global only":  routes.GlobalOnly,
	"single":       routes.Single,
}

// Change is a model learned for a route.
type Change struct {
	Route string
	From  routes.Model
	To    routes.Model
}

// Learn records in the annotations the bucket models that runs settled. Only
// the model is kept, never a limit or a window. A run wins over what the
// annotations said: it is a measure, the annotation was an earlier one.
func Learn(annotationsPath string, reports ...*engine.Report) ([]Change, error) {
	raw, err := os.ReadFile(annotationsPath)
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", annotationsPath, err)
	}
	current := map[routes.Model][]string{}
	if m, ok := doc["models"]; ok {
		if err := json.Unmarshal(m, &current); err != nil {
			return nil, fmt.Errorf("%s: models: %w", annotationsPath, err)
		}
	}

	byRoute := map[string]routes.Model{}
	for model, list := range current {
		for _, route := range list {
			byRoute[route] = model
		}
	}
	var changes []Change
	for _, b := range engine.Merge(reports...).Buckets {
		model, ok := models[b.Model]
		if !ok {
			continue
		}
		for _, route := range b.Routes {
			if byRoute[route] != model {
				changes = append(changes, Change{Route: route, From: byRoute[route], To: model})
				byRoute[route] = model
			}
		}
	}
	families, familyChanges, err := learnFamilies(doc, reports...)
	if err != nil {
		return nil, err
	}
	changes = append(changes, familyChanges...)
	if len(changes) == 0 {
		return nil, nil
	}
	if families != nil {
		doc["families"] = families
	}

	next := map[routes.Model][]string{}
	for route, model := range byRoute {
		next[model] = append(next[model], route)
	}
	for _, list := range next {
		sort.Strings(list)
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	doc["models"] = encoded
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Route < changes[j].Route })
	return changes, os.WriteFile(annotationsPath, append(out, '\n'), 0o644)
}

type family struct {
	Note   string   `json:"note"`
	Routes []string `json:"routes"`
}

// learnFamilies records the routes runs saw share one bucket. A group that
// overlaps a family already written extends it; another one becomes a family
// named after Discord's identifier of the rule, which is not a measure.
func learnFamilies(doc map[string]json.RawMessage, reports ...*engine.Report) (json.RawMessage, []Change, error) {
	known := map[string]family{}
	if raw, ok := doc["families"]; ok {
		if err := json.Unmarshal(raw, &known); err != nil {
			return nil, nil, fmt.Errorf("families: %w", err)
		}
	}
	owner := map[string]string{}
	for name, f := range known {
		for _, r := range f.Routes {
			owner[r] = name
		}
	}
	var changes []Change
	for _, b := range engine.Merge(reports...).Buckets {
		if len(b.Routes) < 2 {
			continue
		}
		name := ""
		for _, r := range b.Routes {
			if owner[r] != "" {
				name = owner[r]
				break
			}
		}
		if name == "" {
			name = "bucket-" + b.Bucket[:min(8, len(b.Bucket))]
			known[name] = family{Note: "Discord counts these routes in one bucket, as runs against it showed."}
		}
		f := known[name]
		for _, r := range b.Routes {
			if owner[r] == "" {
				owner[r] = name
				f.Routes = append(f.Routes, r)
				changes = append(changes, Change{Route: r, To: routes.Model("family " + name)})
			}
		}
		sort.Strings(f.Routes)
		known[name] = f
	}
	if len(changes) == 0 {
		return nil, nil, nil
	}
	raw, err := json.Marshal(known)
	return raw, changes, err
}
