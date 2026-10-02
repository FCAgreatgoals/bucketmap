package engine

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Discord documents one global limit for requests that carry no token,
// applied to the IP address they come from rather than to a bot: "If no
// authorization header is provided, then the limit is applied to the IP
// address" (/developers/topics/rate-limits#global-rate-limit). Webhooks called
// by their URL are such requests, and a proxy that never counts them sends a
// logging bot's bursts straight into it.
//
// Nothing in the headers announces it. The only way to see it is to go past
// it, which this group does on purpose, and only when asked: a burst of reads
// without the bot's token, spread over enough webhooks that none of them
// reaches its own bucket, until Discord refuses one. It costs a handful of
// 429s, and for the time Discord asks to wait, every request without a token
// leaving the IP is refused, whoever sends it.

const (
	// burstChannels are created for the burst, each holding as many
	// webhooks as Discord allows in one channel.
	burstChannels   = 3
	webhooksPerChan = 15
	// burstWebhooks is how many webhooks the burst is spread over. Each one
	// takes a few requests at once before its own bucket runs out: twenty
	// of them let a hundred requests through within a second without a
	// refusal, forty-five carry over two hundred. More is out of reach in one
	// run: Discord refuses webhook creations past about fifty, in the shared
	// scope, though the headers announce no limit.
	burstWebhooks = burstChannels * webhooksPerChan
	// burstPerWebhook caps the requests sent to one webhook in a burst. A
	// webhook's bucket stops it earlier when it is smaller.
	burstPerWebhook = 5
	// readBurst and sendBurst name the requests of a burst in the report.
	readBurst = "anonymous read burst"
	sendBurst = "anonymous send burst"
	// confirmUnder is the longest refusal a second burst is sent after. The
	// second one only confirms the first: past this, it would block the IP
	// as long again for nothing new.
	confirmUnder = 2 * time.Minute
)

// measureIPGlobal creates the webhooks, sends a burst, and a second one once
// Discord's wait is over, to see the first was no accident, then deletes them.
func (s *scenario) measureIPGlobal() {
	if !s.cfg.IPGlobal {
		s.step("measure the global limit per IP", func() error { return skip("needs -ip-global") })
		return
	}
	// Channels of their own: the other groups' channels already hold
	// webhooks, and a channel holds fifteen at most.
	var channels []string
	for i := 1; i <= burstChannels; i++ {
		name := fmt.Sprintf("create burst channel %d", i)
		s.step(name, func() error {
			var ch object
			err := s.do(name, "POST", "/guilds/{guild_id}/channels", s.g()+"/channels", map[string]any{"name": fmt.Sprintf("%s-burst-%d", s.cfg.Marker, i), "type": 0, "parent_id": nilIfEmpty(s.category)}, &ch)
			if ch.ID != "" {
				channels = append(channels, ch.ID)
				s.extra = append(s.extra, named{fmt.Sprintf("burst channel %d", i), ch.ID})
			}
			return err
		})
	}
	for i := 1; i <= burstWebhooks; i++ {
		var ch string
		if n := (i - 1) / webhooksPerChan; n < len(channels) {
			ch = channels[n]
		}
		name := fmt.Sprintf("create burst webhook %d", i)
		s.step(name, func() error {
			if err := s.need(ch); err != nil {
				return err
			}
			var w object
			err := s.do(name, "POST", "/channels/{channel_id}/webhooks", "/channels/"+ch+"/webhooks", map[string]any{"name": fmt.Sprintf("%s-burst-%d", s.cfg.Marker, i)}, &w)
			if w.ID != "" && w.Token != "" {
				s.burstHooks = append(s.burstHooks, w)
			}
			return err
		})
	}

	s.step("measure the global limit per IP", func() error {
		if len(s.burstHooks) < burstWebhooks/2 {
			return skip(fmt.Sprintf("only %d webhooks could be created", len(s.burstHooks)))
		}
		for i := 1; i <= 2; i++ {
			refused, wait := s.c.burst(fmt.Sprintf("%s %d", readBurst, i), s.burstHooks, "GET", nil)
			// Wait out Discord's refusal, and the webhooks' own buckets, so
			// the second burst starts as the first did and the rest of the
			// run is not held up.
			if !s.c.lenient { // a dry run has nothing to wait for
				time.Sleep(max(wait+resetMargin(wait), 2500*time.Millisecond))
			}
			if !refused {
				break
			}
			if i == 1 && wait > confirmUnder {
				s.skipped = append(s.skipped, fmt.Sprintf("%s 2 and %s: Discord asked to wait %s after the first, past the %s another burst is worth", readBurst, sendBurst, wait.Round(time.Second), confirmUnder))
				return nil
			}
		}
		// Then messages, what a logging bot actually sends this way: the
		// limit may hold sends where it let reads through.
		_, wait := s.c.burst(sendBurst, s.burstHooks, "POST", map[string]any{"content": "bucketmap burst"})
		if !s.c.lenient {
			time.Sleep(max(wait+resetMargin(wait), 2500*time.Millisecond))
		}
		return nil
	})

	for i, w := range append([]object{}, s.burstHooks...) {
		name := fmt.Sprintf("delete burst webhook %d", i+1)
		s.step(name, func() error {
			err := s.do(name, "DELETE", "/webhooks/{webhook_id}", "/webhooks/"+w.ID, nil, nil)
			if err == nil {
				s.dropBurstHook(w.ID)
			}
			return err
		})
	}
}

func (s *scenario) dropBurstHook(id string) {
	for i, w := range s.burstHooks {
		if w.ID == id {
			s.burstHooks = append(s.burstHooks[:i], s.burstHooks[i+1:]...)
			return
		}
	}
}

// burst calls every webhook at once without the bot's token, a few times
// each, and stops everything at the first refusal of the global limit. A
// webhook whose own bucket runs out, or refuses on its own, stops alone. It
// tells whether the global limit refused, and how long Discord asked to wait.
func (c *client) burst(step string, hooks []object, method string, body any) (refused bool, wait time.Duration) {
	const route = "/webhooks/{webhook_id}/{webhook_token}"
	var (
		mu      sync.Mutex
		stop    atomic.Bool
		wg      sync.WaitGroup
		results []Result
	)
	start := make(chan struct{})
	for _, h := range hooks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			path := "/webhooks/" + h.ID + "/" + h.Token
			for n := 0; n < burstPerWebhook && !stop.Load(); n++ {
				r := c.send(step, method, route, path, body)
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
				if r.Status == http.StatusTooManyRequests {
					if r.Global || r.Scope == "global" {
						stop.Store(true)
					}
					return
				}
				if r.Error != "" || r.Remaining == 0 {
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	c.last = time.Now()

	for _, r := range results {
		if r.Status == http.StatusTooManyRequests && (r.Global || r.Scope == "global") {
			refused = true
			wait = max(wait, time.Duration(r.ResetAfter*float64(time.Second)))
		}
	}
	c.results = append(c.results, results...)
	return refused, wait
}

// send makes one request of a burst. It touches no state of the client, so
// that the requests of a burst can run side by side.
func (c *client) send(step, method, route, path string, body any) Result {
	r := Result{Step: step, Group: c.group, Method: method, Route: route, Major: majorOf(route, path), Anonymous: true, Deliberate: true}
	payload, contentType, err := encode(body)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	req, err := http.NewRequest(method, c.api+path, payload)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	req.Header.Set("User-Agent", c.agent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	r.At = time.Now()
	res, err := c.http.Do(req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer res.Body.Close()
	answer, _ := io.ReadAll(res.Body)
	r.Status = res.StatusCode
	r.LatencyMs = float64(time.Since(r.At).Microseconds()) / 1000
	if res.StatusCode >= 400 {
		r.Body = truncate(string(answer), 300)
	}
	readHeaders(&r, res.Header)
	return r
}
