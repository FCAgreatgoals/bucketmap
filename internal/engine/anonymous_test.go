package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A burst stops at the first refusal of the global limit, sends no token, and
// counts what passed before it.
func TestBurstStopsAtTheGlobalRefusal(t *testing.T) {
	const allowed = 50
	var (
		mu       sync.Mutex
		served   int
		withAuth int
		perHook  = map[string]int{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			withAuth++
		}
		served++
		if served > allowed {
			w.Header().Set("X-RateLimit-Global", "true")
			w.Header().Set("X-RateLimit-Scope", "global")
			w.Header().Set("Retry-After", "0.8")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"message":"You are being rate limited.","retry_after":0.8,"global":true}`)
			return
		}
		perHook[r.URL.Path]++
		w.Header().Set("X-RateLimit-Bucket", "webhook")
		w.Header().Set("X-RateLimit-Limit", "5")
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(5-perHook[r.URL.Path]))
		w.Header().Set("X-RateLimit-Reset-After", "0.4")
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	var hooks []object
	for i := range burstWebhooks {
		hooks = append(hooks, object{ID: fmt.Sprint(100 + i), Token: "secret"})
	}
	c := newClient(srv.URL, "bot-token", "test", 0)
	refused, wait := c.burst(readBurst+" 1", hooks, "GET", nil)

	if !refused || wait != 800*time.Millisecond {
		t.Fatalf("refused %v, wait %s: want a refusal and 0.8s", refused, wait)
	}
	if withAuth > 0 {
		t.Errorf("%d burst requests carried the bot's token", withAuth)
	}
	bursts := (&Report{Results: c.results}).Bursts()
	if len(bursts) != 1 {
		t.Fatalf("%d bursts summarised, want 1", len(bursts))
	}
	b := bursts[0]
	if b.Accepted != allowed || b.AcceptedBefore > allowed || !b.Refused || b.Scope != "global" {
		t.Errorf("summary %+v: want %d accepted and a global refusal", b, allowed)
	}
	if b.Sent >= burstWebhooks*burstPerWebhook {
		t.Errorf("%d requests sent: the burst did not stop at the refusal", b.Sent)
	}
	if tm := (&Report{Results: c.results}).TooMany(); len(tm) > 0 {
		t.Errorf("a burst's 429 counted as a failure: %d", len(tm))
	}
}

// The remaining of a request without the token, right after requests with it,
// tells their counters apart.
func TestCounterChecks(t *testing.T) {
	route := "/webhooks/{webhook_id}/{webhook_token}"
	pair := func(before, after int, bucket string) []Result {
		return []Result{
			{Step: "check (3/4)", Method: "GET", Route: route, Major: "1/ab", Bucket: "b", Limit: 5, Remaining: before},
			{Step: "check (4/4)", Method: "GET", Route: route, Major: "1/ab", Bucket: bucket, Limit: 5, Remaining: after, Anonymous: true},
		}
	}
	for _, c := range []struct {
		before, after int
		bucket, want  string
	}{
		{2, 1, "b", "one counter"},
		{2, 2, "b", "one counter"},
		{2, 4, "b", "separate counters"},
		{2, 3, "b", "inconclusive"},
		{2, 4, "other", "another bucket"},
	} {
		got := (&Report{Results: pair(c.before, c.after, c.bucket)}).CounterChecks()
		if len(got) != 1 || got[0].Verdict != c.want {
			t.Errorf("remaining %d then %d on %q: got %+v, want %q", c.before, c.after, c.bucket, got, c.want)
		}
	}
}

// A pair of the same request, as every read is sent, checks no counter.
func TestPairsAreNoCounterCheck(t *testing.T) {
	route := "/webhooks/{webhook_id}/{webhook_token}"
	r := &Report{Results: []Result{
		{Step: "read (1/2)", Method: "GET", Route: route, Major: "1", Bucket: "b", Remaining: 4},
		{Step: "read (2/2)", Method: "GET", Route: route, Major: "1", Bucket: "b", Remaining: 3},
	}}
	if got := r.CounterChecks(); len(got) != 0 {
		t.Errorf("a pair read as a counter check: %+v", got)
	}
}

// The dry run sends a read right after sends on the same webhook.
func TestSendThenReadIsChecked(t *testing.T) {
	r := DryRun(true)
	for i := 1; i < len(r.Results); i++ {
		prev, res := r.Results[i-1], r.Results[i]
		if res.Step == "send then read webhook messages (4/4)" && prev.Method == "POST" && res.Method == "GET" && prev.Major == res.Major {
			return
		}
	}
	t.Error("no read follows the sends on the same webhook")
}

// Only webhook token routes are called without the bot's token.
func TestOnlyWebhookTokenRoutesGoAnonymously(t *testing.T) {
	r := DryRun(true)
	anonymous := 0
	for _, res := range r.Results {
		if !res.Anonymous {
			continue
		}
		anonymous++
		if !strings.HasPrefix(res.Route, "/webhooks/{webhook_id}/{webhook_token}") {
			t.Errorf("%s %s went without the bot's token", res.Method, res.Route)
		}
	}
	if anonymous == 0 {
		t.Error("no request went without the bot's token")
	}
	// The counter check needs one request without the token right after
	// requests with it, on the same route.
	followed := false
	for i := 1; i < len(r.Results); i++ {
		prev, res := r.Results[i-1], r.Results[i]
		if res.Anonymous && !res.Deliberate && !prev.Anonymous && prev.Route == res.Route && prev.Major == res.Major {
			followed = true
		}
	}
	if !followed {
		t.Error("no request without the token follows one with it on the same route")
	}
}

// A refusal on a webhook's or a channel's own resource is counted apart: it
// is not the global limit.
func TestBurstsCountOtherRefusalsApart(t *testing.T) {
	at := time.Now()
	r := &Report{Results: []Result{
		{Step: sendBurst, Status: 204, Deliberate: true, At: at},
		{Step: sendBurst, Status: 429, Scope: "shared", Deliberate: true, At: at.Add(time.Millisecond)},
		{Step: sendBurst, Status: 429, Scope: "user", Deliberate: true, At: at.Add(2 * time.Millisecond)},
	}}
	b := r.Bursts()[0]
	if b.Refused || b.Others["shared"] != 1 || b.Others["user"] != 1 {
		t.Errorf("summary %+v: want no global refusal, one shared and one user", b)
	}
}

// The channel's summary counts what its burst took before the first shared
// refusal, and the gaps between accepted steady sends.
func TestChannelLimitSummary(t *testing.T) {
	at := time.Now()
	ms := func(n int) time.Time { return at.Add(time.Duration(n) * time.Millisecond) }
	r := &Report{Results: []Result{
		{Step: channelBurst, Status: 204, Deliberate: true, At: ms(0)},
		{Step: channelBurst, Status: 204, Deliberate: true, At: ms(1)},
		{Step: channelBurst, Status: 429, Scope: "shared", ResetAfter: 1.5, Deliberate: true, At: ms(2)},
		{Step: channelBurst, Status: 204, Deliberate: true, At: ms(3)},
		{Step: channelProbe, Status: 429, Scope: "shared", Deliberate: true, At: ms(100)},
		{Step: channelProbe, Status: 204, Deliberate: true, At: ms(1100)},
		{Step: channelProbe, Status: 204, Deliberate: true, At: ms(2100)},
	}}
	c := r.ChannelLimit()
	if c == nil || c.AcceptedBefore != 2 || c.Accepted != 3 || c.RetryAfter != 1.5 || c.ProbeAccepted != 2 || len(c.Gaps) != 1 || c.Gaps[0] != 1 {
		t.Errorf("summary %+v", c)
	}
	if len(r.Bursts()) != 0 {
		t.Error("the channel's measure is summarised as a burst too")
	}
}

// A shared refusal's wait is in its body: Retry-After says 1 whatever it is.
func TestSharedRefusalWaitComesFromTheBody(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "1")
	h.Set("X-RateLimit-Scope", "shared")
	r := Result{Status: http.StatusTooManyRequests}
	readHeaders(&r, h, []byte(`{"message":"The resource is being rate limited.","retry_after":59.665,"global":false}`))
	if r.ResetAfter != 59.665 {
		t.Errorf("wait %.3f s, want the body's 59.665", r.ResetAfter)
	}
}
