package engine

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// CounterCheck is what the last request of a sequence, sent right after the
// others on the same webhook but another way, said about their counter:
// without the bot's token after requests with it, or a read after sends.
type CounterCheck struct {
	// What follows what, as "GET /route then GET /route without the token".
	Route string
	// Verdict is "one counter", "separate counters", "another bucket", or
	// "inconclusive".
	Verdict string
	// Before is the remaining of the request before the last, After the
	// last one's.
	Before, After int
}

// sequenceStep splits a step named "name (n/total)".
var sequenceStep = regexp.MustCompile(`^(.*) \((\d+)/(\d+)\)$`)

// CounterChecks reads every sequence whose last request differs from the
// one before it, by its token or its route, on the same major parameter. On
// one counter, the remaining keeps going down; on another, it starts again
// near the top.
func (r *Report) CounterChecks() []CounterCheck {
	var out []CounterCheck
	for i := 1; i < len(r.Results); i++ {
		prev, res := r.Results[i-1], r.Results[i]
		m, pm := sequenceStep.FindStringSubmatch(res.Step), sequenceStep.FindStringSubmatch(prev.Step)
		if m == nil || pm == nil || m[1] != pm[1] || m[2] != m[3] || res.Deliberate || prev.Deliberate ||
			res.Major != prev.Major || res.Remaining < 0 || prev.Remaining < 0 ||
			(res.Anonymous == prev.Anonymous && res.Route == prev.Route) {
			continue
		}
		name := prev.Method + " " + prev.Route + " then " + res.Method + " " + res.Route
		if res.Anonymous && !prev.Anonymous {
			name += " without the token"
		}
		c := CounterCheck{Route: name, Before: prev.Remaining, After: res.Remaining}
		switch {
		case res.Bucket != prev.Bucket:
			c.Verdict = "another bucket"
		case res.Remaining <= prev.Remaining:
			// At most one request came back in between.
			c.Verdict = "one counter"
		case res.Remaining >= prev.Remaining+2:
			c.Verdict = "separate counters"
		default:
			c.Verdict = "inconclusive"
		}
		out = append(out, c)
	}
	return out
}

// AnonymousBucket compares the buckets a route answered with, with and
// without the bot's token.
type AnonymousBucket struct {
	Route                 string
	WithToken, WithoutOne []string
	Same                  bool
}

// AnonymousBuckets lists the routes the run called both ways.
func (r *Report) AnonymousBuckets() []AnonymousBucket {
	with, without := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, res := range r.Results {
		if res.Bucket == "" || res.Deliberate {
			continue
		}
		into := with
		if res.Anonymous {
			into = without
		}
		name := res.Method + " " + res.Route
		if into[name] == nil {
			into[name] = map[string]bool{}
		}
		into[name][res.Bucket] = true
	}
	var out []AnonymousBucket
	for name, anon := range without {
		auth, ok := with[name]
		if !ok {
			continue
		}
		b := AnonymousBucket{Route: name, WithToken: keys(auth), WithoutOne: keys(anon)}
		b.Same = strings.Join(b.WithToken, ",") == strings.Join(b.WithoutOne, ",")
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

// Burst is what one burst without the bot's token met.
type Burst struct {
	Step string
	Sent int
	// Accepted counts the answers in 2xx; AcceptedBefore those sent before
	// the first refusal of the global limit, the size of what the IP is
	// allowed at once.
	Accepted, AcceptedBefore int
	// Refused is true when the global limit refused a request.
	Refused bool
	// Within is the time from the first request to the first refusal, or to
	// the last request when nothing was refused.
	Within time.Duration
	// RetryAfter is the longest wait Discord asked for.
	RetryAfter float64
	// Scope and Body are the first refusal's.
	Scope string
	Body  string
	// Others counts refusals that were not the global limit's, by scope: a
	// webhook's own bucket, or a limit on the channel it posts in.
	Others map[string]int
}

// Bursts summarises each burst of the run.
func (r *Report) Bursts() []Burst {
	bySteps := map[string][]Result{}
	var order []string
	for _, res := range r.Results {
		// The channel's measure has a summary of its own.
		if !res.Deliberate || res.Step == channelBurst || res.Step == channelProbe {
			continue
		}
		if bySteps[res.Step] == nil {
			order = append(order, res.Step)
		}
		bySteps[res.Step] = append(bySteps[res.Step], res)
	}
	var out []Burst
	for _, step := range order {
		rs := bySteps[step]
		sort.Slice(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
		b := Burst{Step: step, Sent: len(rs), Others: map[string]int{}}
		var first *Result
		for i, res := range rs {
			if res.Status >= 200 && res.Status < 300 {
				b.Accepted++
			}
			if res.Status == http.StatusTooManyRequests && !res.Global && res.Scope != "global" {
				b.Others[res.Scope]++
			}
			if res.Status == http.StatusTooManyRequests && (res.Global || res.Scope == "global") {
				b.RetryAfter = max(b.RetryAfter, res.ResetAfter)
				if first == nil {
					first = &rs[i]
				}
			}
		}
		end := rs[len(rs)-1].At
		if first != nil {
			b.Refused, b.Scope, b.Body, end = true, first.Scope, first.Body, first.At
			for _, res := range rs {
				if res.At.Before(first.At) && res.Status >= 200 && res.Status < 300 {
					b.AcceptedBefore++
				}
			}
		} else {
			b.AcceptedBefore = b.Accepted
		}
		b.Within = end.Sub(rs[0].At)
		out = append(out, b)
	}
	return out
}

// printAnonymous writes what the run learned of requests without the bot's
// token, if it sent any.
func (r *Report) printAnonymous(w io.Writer) {
	checks, buckets, bursts := r.CounterChecks(), r.AnonymousBuckets(), r.Bursts()
	if len(checks)+len(buckets)+len(bursts) == 0 {
		return
	}
	fmt.Fprintf(w, "\nWithout the bot's token, and shared counters\n")
	for _, b := range buckets {
		verdict := "same bucket as with it"
		if !b.Same {
			verdict = "another bucket than with it"
		}
		fmt.Fprintf(w, "  %-60s %s\n", b.Route, verdict)
	}
	for _, c := range checks {
		fmt.Fprintf(w, "  %s: %s (remaining %d, then %d)\n", c.Route, c.Verdict, c.Before, c.After)
	}
	for _, b := range bursts {
		if b.Refused {
			fmt.Fprintf(w, "  %s: %d accepted in %s, then refused for %s (scope %q), %d of %d accepted in all\n",
				b.Step, b.AcceptedBefore, b.Within.Round(time.Millisecond), humanSeconds(b.RetryAfter), b.Scope, b.Accepted, b.Sent)
			if b.Body != "" && !strings.HasPrefix(strings.TrimSpace(b.Body), "{") {
				// Not Discord's JSON: the refusal came from in front of it.
				fmt.Fprintf(w, "    refused with %q\n", truncate(b.Body, 120))
			}
		} else {
			fmt.Fprintf(w, "  %s: %d of %d accepted in %s, never refused by the global limit\n", b.Step, b.Accepted, b.Sent, b.Within.Round(time.Millisecond))
		}
		var others []string
		for _, scope := range keys(boolKeys(b.Others)) {
			others = append(others, fmt.Sprintf("%d in scope %q", b.Others[scope], scope))
		}
		if len(others) > 0 {
			fmt.Fprintf(w, "    refused on their own resource, not by the global limit: %s\n", strings.Join(others, ", "))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolKeys(m map[string]int) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}
