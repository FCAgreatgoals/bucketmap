// Package indexgen builds routes/index.json from Discord's OpenAPI
// specification, the community documentation of Discord Userdoccers, the
// hand-written annotations, and the routes the scenario covers.
package indexgen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/FCAgreatgoals/bucketmap/internal/engine"
	"github.com/FCAgreatgoals/bucketmap/routes"
)

// Sources are the inputs of a build.
type Sources struct {
	// Spec is Discord's openapi.json, from github.com/discord/discord-api-spec.
	Spec string
	// Userdoccers is the pages directory of
	// github.com/discord-userdoccers/discord-userdoccers.
	Userdoccers string
	Annotations string
}

type annotations struct {
	Models   map[routes.Model][]string `json:"models"`
	Families map[string]struct {
		Note   string   `json:"note"`
		Routes []string `json:"routes"`
	} `json:"families"`
	Notes            map[string][]string `json:"notes"`
	NeedsInteraction []string            `json:"needs_interaction"`
	GlobalExempt     []string            `json:"global_exempt"`
	NotExercised     map[string]string   `json:"not_exercised"`
	OutOfScope       []struct {
		Match  string `json:"match"`
		Reason string `json:"reason"`
	} `json:"out_of_scope"`
}

// Build returns the index, sorted by path then method.
func Build(src Sources) ([]routes.Route, error) {
	var ann annotations
	if err := readJSON(src.Annotations, &ann); err != nil {
		return nil, err
	}
	found, err := fromSpec(src.Spec)
	if err != nil {
		return nil, err
	}
	community, err := fromUserdoccers(src.Userdoccers)
	if err != nil {
		return nil, err
	}
	// The specification wins wherever both describe a route.
	for k, r := range community {
		if _, ok := found[k]; !ok {
			found[k] = r
		}
	}

	byName := map[string]string{} // "METHOD /path" -> key
	for k, r := range found {
		byName[r.Method+" "+r.Path] = k
	}
	lookup := func(name string) (string, error) {
		if k, ok := byName[name]; ok {
			return k, nil
		}
		if k := key(name); found[k] != nil {
			return k, nil
		}
		return "", fmt.Errorf("annotations name %q, which no source describes", name)
	}

	for model, names := range ann.Models {
		for _, name := range names {
			k, err := lookup(name)
			if err != nil {
				return nil, err
			}
			found[k].Model = model
		}
	}
	for family, f := range ann.Families {
		for _, name := range f.Routes {
			k, err := lookup(name)
			if err != nil {
				return nil, err
			}
			found[k].Family = family
			found[k].Notes = append(found[k].Notes, f.Note)
		}
	}
	for name, notes := range ann.Notes {
		k, err := lookup(name)
		if err != nil {
			return nil, err
		}
		found[k].Notes = append(found[k].Notes, notes...)
	}
	for _, name := range ann.GlobalExempt {
		k, err := lookup(name)
		if err != nil {
			return nil, err
		}
		found[k].Global = false
	}

	// Coverage: what the scenario reaches without any flag, then what it
	// reaches only with them or on a community guild.
	plain := engine.Covered(engine.DryRun(false))
	full := engine.Covered(engine.DryRun(true))
	exercised := map[string]bool{}
	for _, name := range plain {
		k, err := lookup(name)
		if err != nil {
			return nil, fmt.Errorf("the scenario calls a route no source describes: %w", err)
		}
		exercised[k] = true
		found[k].Coverage = routes.Exercised
	}
	for _, name := range full {
		k, err := lookup(name)
		if err != nil {
			return nil, fmt.Errorf("the scenario calls a route no source describes: %w", err)
		}
		if !exercised[k] {
			found[k].Coverage = routes.Gated
		}
	}
	for _, name := range ann.NeedsInteraction {
		k, err := lookup(name)
		if err != nil {
			return nil, err
		}
		if found[k].Coverage == "" {
			found[k].Coverage = routes.NeedsInteraction
		}
	}
	for name, reason := range ann.NotExercised {
		k, err := lookup(name)
		if err != nil {
			return nil, err
		}
		if found[k].Coverage == "" {
			found[k].Coverage = routes.NotExercised
			found[k].Notes = append(found[k].Notes, "Not exercised: "+reason)
		}
	}
	for _, r := range found {
		if r.Coverage != "" {
			continue
		}
		if !contains(r.Auth, "bot") {
			r.Coverage = routes.OutOfScope
			r.Notes = append(r.Notes, "Out of scope: needs an OAuth2 token.")
			continue
		}
		for _, o := range ann.OutOfScope {
			if strings.Contains(r.Method+" "+r.Path, o.Match) {
				r.Coverage = routes.OutOfScope
				r.Notes = append(r.Notes, "Out of scope: "+o.Reason)
				break
			}
		}
		if r.Coverage == "" {
			return nil, fmt.Errorf("%s %s is neither exercised nor explained: cover it, or annotate why not", r.Method, r.Path)
		}
	}

	out := make([]routes.Route, 0, len(found))
	for _, r := range found {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out, nil
}

// Write builds the index and writes it as indented JSON.
func Write(src Sources, path string) (int, error) {
	index, err := Build(src)
	if err != nil {
		return 0, err
	}
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return 0, err
	}
	return len(index), os.WriteFile(path, append(raw, '\n'), 0o644)
}

func fromSpec(path string) (map[string]*routes.Route, error) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := readJSON(path, &spec); err != nil {
		return nil, err
	}
	out := map[string]*routes.Route{}
	for p, ops := range spec.Paths {
		for method, raw := range ops {
			method = strings.ToUpper(method)
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				continue
			}
			var op struct {
				OperationID string                `json:"operationId"`
				Security    []map[string][]string `json:"security"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("%s %s: %w", method, p, err)
			}
			var auth []string
			for _, s := range op.Security {
				switch {
				case len(s) == 0:
					auth = appendOnce(auth, "none")
				case s["BotToken"] != nil:
					auth = appendOnce(auth, "bot")
				case s["OAuth2"] != nil:
					auth = appendOnce(auth, "oauth2")
				}
			}
			out[key(method+" "+p)] = newRoute(method, p, humanize(op.OperationID), "discord", auth)
		}
	}
	return out, nil
}

var (
	routeHeader = regexp.MustCompile(`(?s)<RouteHeader(.*?)>(.*?)</RouteHeader>`)
	attrMethod  = regexp.MustCompile(`method="(\w+)"`)
	attrURL     = regexp.MustCompile(`url="([^"]+)"`)
	udParam     = regexp.MustCompile(`\{([a-z_]+)\.([a-z_]+)\}`)
)

// fromUserdoccers reads the routes Discord Userdoccers marks as usable by a
// bot. Its parameters are written {guild.id}; they become {guild_id}, as in
// Discord's specification.
func fromUserdoccers(dir string) (map[string]*routes.Route, error) {
	out := map[string]*routes.Route{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mdx") || strings.HasPrefix(d.Name(), "_") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range routeHeader.FindAllStringSubmatch(string(raw), -1) {
			attrs := m[1]
			if !strings.Contains(attrs, "supportsBot") {
				continue
			}
			method, u := attrMethod.FindStringSubmatch(attrs), attrURL.FindStringSubmatch(attrs)
			if method == nil || u == nil {
				continue
			}
			path := udParam.ReplaceAllStringFunc(strings.TrimSuffix(strings.SplitN(u[1], "?", 2)[0], "/"), func(s string) string {
				parts := udParam.FindStringSubmatch(s)
				if parts[2] == "id" {
					return "{" + parts[1] + "_id}"
				}
				return "{" + parts[2] + "}"
			})
			auth := []string{"bot"}
			if strings.Contains(attrs, "supportsOAuth2") {
				auth = append(auth, "oauth2")
			}
			out[key(method[1]+" "+path)] = newRoute(method[1], path, strings.TrimSpace(m[2]), "userdoccers", auth)
		}
		return nil
	})
	return out, err
}

func newRoute(method, path, name, source string, auth []string) *routes.Route {
	return &routes.Route{
		Method: method, Path: path, Name: name, Source: source, Auth: auth,
		Major: major(path), Global: true, Model: routes.Unknown,
	}
}

// major finds the parameter Discord splits counters by: the first channel,
// guild or webhook in the path, with the webhook's token when it has one.
func major(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		switch p {
		case "{channel_id}", "{guild_id}":
			return strings.Trim(p, "{}")
		case "{webhook_id}":
			if i+1 < len(parts) && parts[i+1] == "{webhook_token}" {
				return "webhook_id+webhook_token"
			}
			return "webhook_id"
		}
	}
	return ""
}

var anyParam = regexp.MustCompile(`\{[^}]*\}`)

// key identifies a route whatever its parameters are named.
func key(name string) string { return anyParam.ReplaceAllString(name, "{}") }

func humanize(operationID string) string {
	if operationID == "" {
		return ""
	}
	s := strings.ReplaceAll(operationID, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func appendOnce(list []string, v string) []string {
	if contains(list, v) {
		return list
	}
	return append(list, v)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
