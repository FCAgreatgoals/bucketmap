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
	if len(changes) == 0 {
		return nil, nil
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
