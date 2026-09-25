// Package engine walks a Discord test guild through every route a bot can
// reach, a few requests each, paced well below every limit, and reports what
// each route answered and which rate limit it fell under.
//
// It is a conformance check, not a load test: requests are spread over the
// whole duration, a bucket that reports nothing left is waited out, and a 429
// is recorded and never retried. Run it once against Discord and once against
// a candidate, a proxy in front of Discord or anything standing in for it,
// then compare the two reports.
package engine

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"
)

// Config is what a run needs.
type Config struct {
	// API is the API root: Discord itself, or the candidate.
	API   string
	Token string
	// Guild is set aside for testing, and its name must contain Marker.
	Guild  string
	Users  []string
	Marker string
	// Duration is how long to spread the run over.
	Duration time.Duration
	// Agent is sent as User-Agent.
	Agent string
	// Only restricts the run to these groups, and those they need. Empty runs
	// them all. See Groups.
	Only []string

	// AllowKick kicks test user 3, who then has to rejoin.
	AllowKick bool
	// AllowBan bans then unbans test user 4, one by one and in bulk, who then
	// has to rejoin.
	AllowBan bool
	// AllowPrune prunes members of the guild inactive for thirty days who
	// hold no role.
	AllowPrune bool
	// AllowGlobalCommands creates, edits and deletes a global command, which
	// every guild of the bot sees for a moment.
	AllowGlobalCommands bool
}

func (c Config) validate() error {
	switch {
	case c.Token == "" || c.Guild == "":
		return errors.New("a token and a guild are required")
	case len(c.Users) != 4:
		return fmt.Errorf("four test user ids are needed, got %d", len(c.Users))
	case c.Marker == "":
		return errors.New("the marker cannot be empty: it is what keeps the run off a real guild")
	}
	_, err := selected(c.Only)
	return err
}

// Run walks the scenario and returns its report. An error means the run could
// not start: the preflight refused the guild. Failed steps are in the report.
func Run(cfg Config) (*Report, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	planned := len(dryRun(true, cfg.Only).Results)
	spacing := cfg.Duration / time.Duration(max(planned, 1))
	spacing = max(spacing, 250*time.Millisecond)
	return run(cfg, newClient(cfg.API, strings.TrimPrefix(cfg.Token, "Bot "), cfg.Agent, spacing))
}

func run(cfg Config, c *client) (*Report, error) {
	s := &scenario{c: c, cfg: cfg}
	r := &Report{API: cfg.API, Started: time.Now(), Spacing: c.spacing.String()}

	runErr := s.run()
	if runErr == nil {
		r.Invite = s.rejoinInvite()
	}
	r.Finished = time.Now()
	r.Results = c.results
	r.Failures = s.failures
	r.Skipped = s.skipped
	r.Buckets = modelBuckets(c.results)
	return r, runErr
}

// DryRun walks the scenario against a local responder that answers every
// request with success. Nothing leaves the machine. With full, every flag is
// on and the guild is a community guild; without, neither. It is how the index
// knows which routes the scenario covers, and how a run knows how many
// requests to spread over its duration.
func DryRun(full bool) *Report { return dryRun(full, nil) }

func dryRun(full bool, only []string) *Report {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { respond(w, r, full) }))
	defer srv.Close()

	cfg := Config{
		API: srv.URL, Token: "dry-run", Guild: dryGuild, Marker: "bucketmap",
		Users:     []string{"1001", "1002", "1003", "1004"},
		Agent:     "bucketmap dry run",
		AllowKick: full, AllowBan: full, AllowPrune: full, AllowGlobalCommands: full,
		Only: only,
	}
	c := newClient(srv.URL, cfg.Token, cfg.Agent, 0)
	c.lenient = true
	r, _ := run(cfg, c)
	return r
}

// Covered lists the routes a report exercised, as "METHOD /template".
func Covered(r *Report) []string {
	seen := map[string]bool{}
	for _, res := range r.Results {
		seen[res.Method+" "+res.Route] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const dryGuild = "900"

// dryObject carries every field the scenario reads from an answer, so that
// every step finds what it needs.
const dryObject = `{"id":"1","name":"bucketmap","token":"t","code":"c","sound_id":"1","event_exception_id":"1",` +
	`"webhook_id":"1","features":%s,"roles":[],"permissions":"8",` +
	`"attachments":[{"id":"0","url":"https://cdn.invalid/a.txt","upload_filename":"u/a.txt"}],` +
	`"available_tags":[{"id":"1","name":"bucketmap"}],"sticker_packs":[{"id":"1"}]}`

func respond(w http.ResponseWriter, r *http.Request, community bool) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.SplitN(r.URL.Path, "?", 2)[0]
	switch {
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/roles"):
		fmt.Fprintf(w, `[{"id":%q,"permissions":"8","position":0},{"id":"1","permissions":"0","position":1}]`, dryGuild)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/channels"):
		fmt.Fprint(w, `[{"id":"1","type":0}]`)
	case r.Method == http.MethodGet && (strings.HasSuffix(path, "/commands") || strings.HasSuffix(path, "/metadata")):
		fmt.Fprint(w, `[{"id":"1","name":"bucketmap"}]`)
	default:
		features := `[]`
		if community {
			features = `["COMMUNITY"]`
		}
		fmt.Fprintf(w, dryObject, features)
	}
}
