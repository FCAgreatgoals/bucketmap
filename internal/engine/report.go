package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// Report is what a run returns, and what it writes to disk.
type Report struct {
	API      string        `json:"api"`
	Started  time.Time     `json:"started"`
	Finished time.Time     `json:"finished"`
	Spacing  string        `json:"spacing"`
	Results  []Result      `json:"results"`
	Failures []string      `json:"failures"`
	Skipped  []string      `json:"skipped"`
	Invite   string        `json:"rejoin_invite,omitempty"`
	Buckets  []BucketModel `json:"buckets"`
}

// BucketModel is what the run learned about one Discord bucket.
type BucketModel struct {
	Bucket string   `json:"bucket"`
	Routes []string `json:"routes"`
	Limit  int      `json:"limit"`
	// Model is "token bucket", "fixed window", "global only" for a route
	// Discord does not limit on its own, or "unknown" when no two requests of
	// the run fell within one window of each other.
	Model string `json:"model"`
	// WindowSeconds is the time the bucket takes to refill fully, when the
	// model is known. For an unknown model it is only what is sure: an upper
	// bound for a fixed window, one token's time for a token bucket.
	WindowSeconds float64 `json:"window_seconds"`
	// FirstBackSeconds is the Reset-After of a request that opened the
	// bucket: the whole window of a fixed window, one token of a token
	// bucket. Zero when no request of the run opened it.
	FirstBackSeconds float64 `json:"first_back_seconds,omitempty"`
	Pairs            int     `json:"pairs"`
}

// A route Discord does not limit on its own answers a limit this large and a
// reset this short: only the global limit holds it back.
const (
	globalOnlyLimit = 1000
	globalOnlyReset = 0.01
)

// modelBuckets classifies each bucket from consecutive requests on it.
//
// X-RateLimit-Reset-After is the time until the bucket is full again, so the
// instant it names stays put across the requests of a fixed window and moves
// forward by a constant step on a token bucket. Two requests close together are
// enough to tell them apart.
func modelBuckets(results []Result) []BucketModel {
	type key struct{ bucket, major string }
	samples := map[key][]Result{}
	routes := map[string]map[string]bool{}
	for _, r := range results {
		if r.Bucket == "" || r.Remaining < 0 {
			continue
		}
		k := key{r.Bucket, r.Major}
		samples[k] = append(samples[k], r)
		if routes[r.Bucket] == nil {
			routes[r.Bucket] = map[string]bool{}
		}
		routes[r.Bucket][r.Method+" "+r.Route] = true
	}

	byBucket := map[string]*BucketModel{}
	for k, rs := range samples {
		m := byBucket[k.bucket]
		if m == nil {
			m = &BucketModel{Bucket: k.bucket, Model: "unknown"}
			for route := range routes[k.bucket] {
				m.Routes = append(m.Routes, route)
			}
			sort.Strings(m.Routes)
			byBucket[k.bucket] = m
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })

		// The reference is the reset-after of a request that opened the
		// bucket: on a token bucket it is the time one token takes to come
		// back, on a fixed window the window itself. Later requests are no
		// reference, since a token bucket's reset-after grows with each one.
		// A run can start on a bucket already in use, with no opening
		// request; the step between two requests then stands in for it.
		reference := 0.0
		for _, r := range rs {
			if r.Limit > m.Limit {
				m.Limit = r.Limit
			}
			if r.Remaining == r.Limit-1 && r.ResetAfter > reference {
				reference = r.ResetAfter
			}
		}

		for i := 1; i < len(rs); i++ {
			prev, r := rs[i-1], rs[i]
			gap := r.At.Sub(prev.At).Seconds()
			if r.Remaining != prev.Remaining-1 || gap >= prev.ResetAfter || prev.ResetAfter <= 0 {
				continue
			}
			// How far the instant of the next full refill moved: nothing on a
			// fixed window, one token's worth on a token bucket. The tolerance
			// absorbs the network time folded into the two timestamps.
			step := (gap + r.ResetAfter) - prev.ResetAfter
			m.Pairs++
			fixed := math.Abs(step) <= 0.05*prev.ResetAfter+0.05
			if reference > 0 {
				// With one token's time known, the step is held to both
				// hypotheses and the nearer wins: a fixed threshold took
				// 0.303 s of network time on a five second window for a
				// token.
				fixed = math.Abs(step) < math.Abs(step-reference)
			}
			if fixed {
				m.Model = "fixed window"
				continue
			}
			m.Model = "token bucket"
			perToken := reference
			if perToken == 0 {
				perToken = step
			}
			m.WindowSeconds = perToken * float64(m.Limit)
		}

		m.FirstBackSeconds = math.Max(m.FirstBackSeconds, reference)
		globalOnly := len(rs) > 0
		for _, r := range rs {
			if r.Limit < globalOnlyLimit || r.ResetAfter > globalOnlyReset {
				globalOnly = false
			}
		}
		if globalOnly && m.Model == "unknown" {
			m.Model = "global only"
			m.WindowSeconds = 0
			continue
		}

		if m.Model != "token bucket" {
			window := reference
			if window == 0 {
				for _, r := range rs {
					window = math.Max(window, r.ResetAfter)
				}
			}
			m.WindowSeconds = math.Max(m.WindowSeconds, window)
		}
	}

	out := make([]BucketModel, 0, len(byBucket))
	for _, m := range byBucket {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Routes[0] < out[j].Routes[0] })
	return out
}

func (r *Report) TooMany() []Result {
	var out []Result
	for _, res := range r.Results {
		if res.Status == 429 {
			out = append(out, res)
		}
	}
	return out
}

// Print writes a summary of the run.
func (r *Report) Print(w io.Writer) {
	statuses := map[int]int{}
	for _, res := range r.Results {
		statuses[res.Status]++
	}
	codes := make([]int, 0, len(statuses))
	for code := range statuses {
		codes = append(codes, code)
	}
	sort.Ints(codes)

	fmt.Fprintf(w, "\n%d requests to %s in %s (one every %s)\n", len(r.Results), r.API, r.Finished.Sub(r.Started).Round(time.Second), r.Spacing)
	for _, code := range codes {
		fmt.Fprintf(w, "  %3d  x%d\n", code, statuses[code])
	}

	fmt.Fprintf(w, "\nBuckets met\n")
	for _, b := range r.Buckets {
		shared := ""
		if len(b.Routes) > 1 {
			shared = fmt.Sprintf("  shared by %d routes", len(b.Routes))
		}
		var limit string
		switch b.Model {
		case "global only":
			limit = "no limit of its own"
		case "unknown":
			// Only what is sure: without two requests in a row, a window
			// and a token look the same.
			if b.FirstBackSeconds > 0 {
				limit = fmt.Sprintf("%d, first back in %s", b.Limit, humanSeconds(b.FirstBackSeconds))
			} else {
				limit = fmt.Sprintf("%d, full within %s", b.Limit, humanSeconds(b.WindowSeconds))
			}
		default:
			limit = fmt.Sprintf("%d per %s", b.Limit, humanSeconds(b.WindowSeconds))
		}
		fmt.Fprintf(w, "  %-12s %-28s %s%s\n", b.Model, limit, b.Routes[0], shared)
	}

	if tm := r.TooMany(); len(tm) > 0 {
		fmt.Fprintf(w, "\n429 received, %d: each is a request that should never have been sent\n", len(tm))
		for _, res := range tm {
			fmt.Fprintf(w, "  %s  %s %s  scope=%s\n", res.Step, res.Method, res.Route, res.Scope)
		}
	}
	if len(r.Failures) > 0 {
		fmt.Fprintf(w, "\nFailed steps, %d\n", len(r.Failures))
		for _, f := range r.Failures {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "\nSkipped after an earlier failure: %s\n", strings.Join(r.Skipped, ", "))
	}
	if r.Invite != "" {
		fmt.Fprintf(w, "\nKicked or banned test users can rejoin with %s\n", r.Invite)
	}
}

func humanSeconds(s float64) string {
	switch {
	case s >= 3600:
		return fmt.Sprintf("%.1fh", s/3600)
	case s >= 60:
		return fmt.Sprintf("%.1fmin", s/60)
	default:
		return fmt.Sprintf("%.3fs", s)
	}
}

// LoadReport reads a report written by a run.
func LoadReport(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	return &r, json.Unmarshal(raw, &r)
}

// Compare puts a run straight against Discord and a run against a candidate
// side by side: a proxy in front of Discord, or anything that stands in for it.
// The candidate passes when every step answers the same, and nothing reached a
// 429.
func Compare(w io.Writer, direct, candidate *Report) bool {
	status := func(r *Report) map[string]int {
		out := map[string]int{}
		for _, res := range r.Results {
			out[res.Step] = res.Status
		}
		return out
	}
	a, b := status(direct), status(candidate)

	var differ []string
	for step, code := range a {
		if other, ok := b[step]; ok && other != code {
			differ = append(differ, fmt.Sprintf("  %s: %d direct, %d candidate", step, code, other))
		}
	}
	sort.Strings(differ)

	fmt.Fprintf(w, "direct    %s: %d requests, %d 429\n", direct.API, len(direct.Results), len(direct.TooMany()))
	fmt.Fprintf(w, "candidate %s: %d requests, %d 429\n", candidate.API, len(candidate.Results), len(candidate.TooMany()))
	fmt.Fprintf(w, "median latency: %.0f ms direct, %.0f ms candidate\n", medianLatency(direct), medianLatency(candidate))
	if len(differ) > 0 {
		fmt.Fprintf(w, "\nsteps that answered differently\n%s\n", strings.Join(differ, "\n"))
	}
	ok := len(differ) == 0 && len(candidate.TooMany()) == 0
	if ok {
		fmt.Fprintln(w, "\nsame answers on every step, and no 429 on the candidate")
	}
	return ok
}

func medianLatency(r *Report) float64 {
	if len(r.Results) == 0 {
		return 0
	}
	values := make([]float64, 0, len(r.Results))
	for _, res := range r.Results {
		values = append(values, res.LatencyMs)
	}
	sort.Float64s(values)
	return values[len(values)/2]
}
