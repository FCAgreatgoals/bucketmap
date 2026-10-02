package engine

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Discord limits the messages sent through webhooks per channel, all its
// webhooks together, and says so nowhere: the headers describe each webhook's
// own bucket, and a burst through fifteen webhooks of one channel was refused
// past about thirty, in the shared scope, while those buckets still had room.
//
// A burst finds how much the channel takes at once. Steady sends right after,
// spread over the channel's webhooks so that none of them reaches its own
// bucket, show how fast it comes back: a fixed window lets a whole burst
// through again at once, a token bucket one at a time. Shared refusals are no
// invalid requests: Discord counts none of them towards a ban.

const (
	channelBurst = "channel send burst"
	channelProbe = "channel send probe"
	// probeEvery is the pace of the steady sends: fast enough to see a
	// refill of several a second, slow enough that each webhook of the
	// channel stays far from its own bucket.
	probeEvery = 100 * time.Millisecond
	probeFor   = 10 * time.Second
)

// measureChannelLimit sends a burst through the webhooks of one channel, then
// steady sends through them.
func (s *scenario) measureChannelLimit(hooks []object) error {
	if len(hooks) < 8 {
		return skip(fmt.Sprintf("only %d webhooks in one channel", len(hooks)))
	}
	if !s.c.lenient {
		// Let the send burst's refusals and the webhooks' buckets pass.
		time.Sleep(5 * time.Second)
	}
	body := map[string]any{"content": "bucketmap channel burst"}
	if refused, wait := s.c.burst(channelBurst, hooks, "POST", body); refused {
		// The IP's global limit refused before the channel's could: there is
		// nothing to learn about the channel until it lifts.
		if !s.c.lenient {
			time.Sleep(wait + resetMargin(wait))
		}
		return skip("the global limit refused the burst first")
	}

	const route = "/webhooks/{webhook_id}/{webhook_token}"
	start := time.Now()
	for i := 0; time.Since(start) < probeFor; i++ {
		if s.c.lenient && i == 3 {
			// A dry run answers at once: a few sends show the steps work.
			break
		}
		h := hooks[i%len(hooks)]
		r := s.c.send(channelProbe, "POST", route, "/webhooks/"+h.ID+"/"+h.Token, body)
		s.c.results = append(s.c.results, r)
		if r.Status == http.StatusTooManyRequests && (r.Global || r.Scope == "global") {
			break
		}
		if !s.c.lenient {
			time.Sleep(time.Until(start.Add(time.Duration(i+1) * probeEvery)))
		}
	}
	s.c.last = time.Now()
	return nil
}

// ChannelLimit is what the channel's burst and steady sends met.
type ChannelLimit struct {
	// Sent and Accepted count the burst; AcceptedBefore those accepted
	// before its first shared refusal, what the channel takes at once.
	Sent, Accepted, AcceptedBefore int
	// RetryAfter is the longest wait a shared refusal of the burst asked for.
	RetryAfter float64

	// ProbeSent and ProbeAccepted count the steady sends, over ProbeFor.
	ProbeSent, ProbeAccepted int
	ProbeFor                 time.Duration
	// Gaps are the times between two accepted steady sends, in seconds.
	Gaps []float64
}

// ChannelLimit summarises the channel measure, or nil when the run made none.
func (r *Report) ChannelLimit() *ChannelLimit {
	var burst, probe []Result
	for _, res := range r.Results {
		switch res.Step {
		case channelBurst:
			burst = append(burst, res)
		case channelProbe:
			probe = append(probe, res)
		}
	}
	if len(burst) == 0 {
		return nil
	}
	byTime := func(rs []Result) {
		sort.Slice(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
	}
	byTime(burst)
	byTime(probe)

	c := &ChannelLimit{Sent: len(burst), ProbeSent: len(probe)}
	var firstRefusal time.Time
	for _, res := range burst {
		if accepted(res) {
			c.Accepted++
		}
		if res.Status == http.StatusTooManyRequests && res.Scope == "shared" {
			c.RetryAfter = math.Max(c.RetryAfter, res.ResetAfter)
			if firstRefusal.IsZero() {
				firstRefusal = res.At
			}
		}
	}
	for _, res := range burst {
		if accepted(res) && (firstRefusal.IsZero() || res.At.Before(firstRefusal)) {
			c.AcceptedBefore++
		}
	}
	var last time.Time
	for _, res := range probe {
		if !accepted(res) {
			continue
		}
		c.ProbeAccepted++
		if !last.IsZero() {
			c.Gaps = append(c.Gaps, res.At.Sub(last).Seconds())
		}
		last = res.At
	}
	if len(probe) > 0 {
		c.ProbeFor = probe[len(probe)-1].At.Sub(probe[0].At)
	}
	return c
}

func accepted(res Result) bool { return res.Status >= 200 && res.Status < 300 }

func (r *Report) printChannelLimit(w io.Writer) {
	c := r.ChannelLimit()
	if c == nil {
		return
	}
	fmt.Fprintf(w, "\nWebhook sends in one channel\n")
	if c.RetryAfter == 0 {
		fmt.Fprintf(w, "  burst: %d of %d accepted, never refused by the channel\n", c.Accepted, c.Sent)
	} else {
		fmt.Fprintf(w, "  burst: %d accepted before the channel refused, for up to %s, %d of %d accepted in all\n",
			c.AcceptedBefore, humanSeconds(c.RetryAfter), c.Accepted, c.Sent)
	}
	if c.ProbeSent == 0 {
		return
	}
	rate := 0.0
	if c.ProbeFor > 0 {
		rate = float64(c.ProbeAccepted) / c.ProbeFor.Seconds()
	}
	fmt.Fprintf(w, "  steady sends, one every %s: %d of %d accepted over %s, %.2f a second\n",
		probeEvery, c.ProbeAccepted, c.ProbeSent, c.ProbeFor.Round(100*time.Millisecond), rate)
	if len(c.Gaps) > 0 {
		// Repeated gaps are written once with their count: 0.1x40.
		var gaps []string
		for i := 0; i < len(c.Gaps); {
			v := fmt.Sprintf("%.1f", c.Gaps[i])
			n := 1
			for i+n < len(c.Gaps) && fmt.Sprintf("%.1f", c.Gaps[i+n]) == v {
				n++
			}
			if n > 1 {
				v += fmt.Sprintf("x%d", n)
			}
			gaps = append(gaps, v)
			i += n
		}
		// Even gaps are a token bucket's refill; runs of short gaps between
		// long ones, a window opening again.
		fmt.Fprintf(w, "  seconds between accepted sends: %s\n", strings.Join(gaps, " "))
	}
}
