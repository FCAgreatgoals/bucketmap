// Package routes indexes every Discord REST route a bot can reach, with what
// is known of its rate limit: the major parameter that splits its counters,
// the model its bucket follows, the routes that share its bucket, and the
// special cases around it.
//
// The index is index.json, embedded here and readable on its own from any
// language. It carries no measured limit or window: those differ from one
// application to another and change over time, and a run of the bucketmap
// command against Discord reports them for the bot that runs it.
package routes

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

// Model is how a bucket refills.
type Model string

const (
	// TokenBucket refills one request at a time: X-RateLimit-Reset-After
	// announces when the bucket is full again, and grows with each request.
	TokenBucket Model = "token_bucket"
	// FixedWindow refills all at once, when the window ends: the instant
	// X-RateLimit-Reset-After points to stays put across requests.
	FixedWindow Model = "fixed_window"
	// GlobalOnly is a route Discord does not limit on its own: it answers a
	// limit of a thousand that resets within milliseconds, and only the
	// global limit holds it back.
	GlobalOnly Model = "global_only"
	// Unknown is a route no run has seen two requests of within one window.
	Unknown Model = "unknown"
)

// Coverage says whether the bucketmap scenario exercises a route, and if not,
// why.
type Coverage string

const (
	// Exercised routes are called by every run of the scenario.
	Exercised Coverage = "exercised"
	// Gated routes are exercised only under a flag, or only on a guild that
	// has what they need, such as the COMMUNITY feature.
	Gated Coverage = "gated"
	// NeedsInteraction routes answer an interaction, which only a user can
	// start.
	NeedsInteraction Coverage = "needs_interaction"
	// NotExercised routes are reachable with a bot token but left out, for the
	// reason given in the route's notes.
	NotExercised Coverage = "not_exercised"
	// OutOfScope routes need something a bot token alone does not give: an
	// OAuth2 token, a game SDK, monetization, or a user account.
	OutOfScope Coverage = "out_of_scope"
)

// Route is one method and path template.
type Route struct {
	Method string `json:"method"`
	// Path is the template, with Discord's parameter names, e.g.
	// /channels/{channel_id}/messages/{message_id}.
	Path string `json:"path"`
	Name string `json:"name"`
	// Source is "discord" for routes of Discord's OpenAPI specification, and
	// "userdoccers" for routes only the community documents.
	Source string `json:"source"`
	// Auth lists what the route accepts: "bot", "oauth2", "none".
	Auth []string `json:"auth"`
	// Major is the parameter that gives each value its own counter:
	// channel_id, guild_id, webhook_id, or webhook_id+webhook_token. Empty
	// when every call shares one counter per application.
	Major string `json:"major,omitempty"`
	// Global is false for routes exempt from the bot's global limit.
	Global bool  `json:"global"`
	Model  Model `json:"model"`
	// Family names routes Discord counts together, in one bucket.
	Family   string   `json:"family,omitempty"`
	Coverage Coverage `json:"coverage"`
	Notes    []string `json:"notes,omitempty"`
}

//go:embed index.json
var indexJSON []byte

var (
	loadOnce sync.Once
	all      []Route
	loadErr  error
)

// All returns every indexed route.
func All() ([]Route, error) {
	loadOnce.Do(func() { loadErr = json.Unmarshal(indexJSON, &all) })
	return all, loadErr
}

// Match finds the route a concrete request falls under, from its method and
// path. The path may carry the /api or /api/vN prefix and a query string.
func Match(method, path string) (Route, bool) {
	routes, err := All()
	if err != nil {
		return Route{}, false
	}
	parts := split(trimPrefix(path))
	best, bestScore := Route{}, -1
	for _, r := range routes {
		if r.Method != method {
			continue
		}
		if score, ok := matches(split(r.Path), parts); ok && score > bestScore {
			best, bestScore = r, score
		}
	}
	return best, bestScore >= 0
}

// matches compares a template with a concrete path, and scores the match by
// its literal segments, so that /users/@me wins over /users/{user_id}.
func matches(template, parts []string) (int, bool) {
	if len(template) != len(parts) {
		return 0, false
	}
	score := 0
	for i, t := range template {
		switch {
		case isParam(t):
		case t == parts[i]:
			score++
		default:
			return 0, false
		}
	}
	return score, true
}

// MajorValue extracts the value of a route's major parameter from a concrete
// path: what separates one counter of the route's bucket from another.
func (r Route) MajorValue(path string) string {
	if r.Major == "" {
		return ""
	}
	template, parts := split(r.Path), split(trimPrefix(path))
	if len(template) != len(parts) {
		return ""
	}
	var values []string
	for _, name := range strings.Split(r.Major, "+") {
		for i, t := range template {
			if t == "{"+name+"}" {
				values = append(values, parts[i])
			}
		}
	}
	return strings.Join(values, "/")
}

func isParam(s string) bool { return strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") }

func split(path string) []string { return strings.Split(strings.Trim(path, "/"), "/") }

func trimPrefix(path string) string {
	path = strings.SplitN(path, "?", 2)[0]
	if !strings.HasPrefix(path, "/api/") {
		return path
	}
	path = strings.TrimPrefix(path, "/api")
	head, tail, found := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if found && len(head) > 1 && head[0] == 'v' && strings.Trim(head[1:], "0123456789") == "" {
		return "/" + tail
	}
	return path
}
