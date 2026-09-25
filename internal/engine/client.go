package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Result is one request as it happened.
type Result struct {
	Step       string    `json:"step"`
	Group      string    `json:"group,omitempty"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Major      string    `json:"major,omitempty"`
	Status     int       `json:"status"`
	At         time.Time `json:"at"`
	LatencyMs  float64   `json:"latency_ms"`
	Bucket     string    `json:"bucket,omitempty"`
	Limit      int       `json:"limit,omitempty"`
	Remaining  int       `json:"remaining"`
	ResetAfter float64   `json:"reset_after,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Global     bool      `json:"global,omitempty"`
	Error      string    `json:"error,omitempty"`
	// Body is the start of the answer when it is not a success: which error
	// Discord gave is what a candidate has to reproduce.
	Body string `json:"body,omitempty"`

	// With Config.Bodies, the exchange itself: the concrete path, what was
	// sent and what came back, tokens hidden. What a candidate's answers are
	// held to, field by field.
	Path     string          `json:"path,omitempty"`
	Request  json.RawMessage `json:"request,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
}

// bucketState is what the last response of a bucket said about it.
type bucketState struct {
	remaining int
	resetAt   time.Time
	// window is the Reset-After of the last answer, which sizes the margin.
	window time.Duration
}

// client sends the scenario's requests one at a time, paced well below every
// limit it learns of.
//
// It never races a limit. Requests are spaced by a fixed interval, and when a
// bucket reports nothing left the next request on it waits for its reset. A
// 429 is recorded as a failure and never retried: whatever sits between the
// engine and Discord, a 429 means a request went out that Discord refused.
type client struct {
	api     string
	token   string
	agent   string
	http    *http.Client
	spacing time.Duration
	last    time.Time

	// routeBuckets maps a route and its major parameter to the Discord bucket
	// last reported for it, so that routes sharing a bucket share its state.
	routeBuckets map[string]string
	buckets      map[string]*bucketState

	// group names the scenario group being run, recorded on each result.
	group string

	// bodies keeps each exchange in its result.
	bodies bool

	// lenient ignores answers that do not decode, for a dry run, where every
	// route answers the same placeholder.
	lenient bool

	results []Result
}

func newClient(api, token, agent string, spacing time.Duration) *client {
	return &client{
		api:          strings.TrimRight(api, "/"),
		token:        token,
		agent:        agent,
		http:         &http.Client{Timeout: 30 * time.Second},
		spacing:      spacing,
		routeBuckets: map[string]string{},
		buckets:      map[string]*bucketState{},
	}
}

// call describes one request of the scenario.
type call struct {
	step   string
	method string
	route  string // template, as the index names it, e.g. /channels/{channel_id}/messages
	path   string // concrete path
	body   any
	// immediate skips the spacing, to follow the previous request at once.
	// The scenario uses it for pairs of requests on one bucket, which is what
	// tells a token bucket from a fixed window, and stays two requests.
	immediate bool
	// reason is sent as X-Audit-Log-Reason, so every action the engine takes
	// is recognisable in the guild's audit log.
	reason string
}

// do sends a request and decodes a JSON answer into out when out is not nil.
// A non-2xx status is returned as an error, after being recorded.
func (c *client) do(k call, out any) error {
	major := majorOf(k.route, k.path)
	c.pace(k, major)

	body, contentType, err := encode(k.body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(k.method, c.api+k.path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", c.agent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	reason := k.reason
	if reason == "" {
		reason = "bucketmap: " + k.step
	}
	req.Header.Set("X-Audit-Log-Reason", reason)

	start := time.Now()
	res, err := c.http.Do(req)
	c.last = time.Now()
	r := Result{Step: k.step, Group: c.group, Method: k.method, Route: k.route, Major: major, At: start}
	if err != nil {
		r.Error = err.Error()
		c.results = append(c.results, r)
		return err
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)

	r.Status = res.StatusCode
	if res.StatusCode >= 400 {
		r.Body = truncate(string(payload), 300)
	}
	if c.bodies {
		r.Path = redact(k.route, k.path)
		r.Request = recordRequest(k.body)
		r.Response = recordResponse(res.Header.Get("Content-Type"), payload)
	}
	r.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	c.observe(&r, res.Header)
	c.results = append(c.results, r)

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return &statusError{status: res.StatusCode, scope: r.Scope, msg: fmt.Sprintf("%s %s: %d %s", k.method, redact(k.route, k.path), res.StatusCode, truncate(string(payload), 200))}
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil && !c.lenient {
			return err
		}
	}
	return nil
}

// statusError is a response outside 2xx.
type statusError struct {
	status int
	scope  string
	msg    string
}

func (e *statusError) Error() string { return e.msg }

// multipartBody is a request body sent as multipart/form-data. Messages put
// their JSON in payload_json and each file in files[n]; stickers use plain
// fields and a single file field.
type multipartBody struct {
	payload any
	fields  map[string]string
	files   []file
}

type file struct {
	field       string // files[n] when empty
	name        string
	contentType string
	data        []byte
}

func encode(body any) (io.Reader, string, error) {
	switch b := body.(type) {
	case nil:
		return nil, "", nil
	case multipartBody:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		if b.payload != nil {
			raw, err := json.Marshal(b.payload)
			if err != nil {
				return nil, "", err
			}
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", `form-data; name="payload_json"`)
			h.Set("Content-Type", "application/json")
			part, err := w.CreatePart(h)
			if err != nil {
				return nil, "", err
			}
			part.Write(raw)
		}
		for _, k := range sortedKeys(b.fields) {
			if err := w.WriteField(k, b.fields[k]); err != nil {
				return nil, "", err
			}
		}
		for i, f := range b.files {
			field := f.field
			if field == "" {
				field = fmt.Sprintf("files[%d]", i)
			}
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, f.name))
			h.Set("Content-Type", f.contentType)
			part, err := w.CreatePart(h)
			if err != nil {
				return nil, "", err
			}
			part.Write(f.data)
		}
		if err := w.Close(); err != nil {
			return nil, "", err
		}
		return &buf, w.FormDataContentType(), nil
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, "", err
		}
		return bytes.NewReader(raw), "application/json", nil
	}
}

// pace waits for the spacing, and for the bucket's reset if it reported
// nothing left.
func (c *client) pace(k call, major string) {
	if !k.immediate {
		if wait := c.spacing - time.Since(c.last); wait > 0 {
			time.Sleep(wait)
		}
	}
	if id, ok := c.routeBuckets[k.method+" "+k.route+" "+major]; ok {
		if b := c.buckets[id]; b != nil && b.remaining == 0 {
			// The margin holds even when the answer took the whole reset to
			// come back: a quarter second bucket was sent into again 0.26 s
			// after its first request, and refused.
			if wait := time.Until(b.resetAt.Add(resetMargin(b.window))); wait > 0 {
				time.Sleep(wait)
			}
		}
	}
}

// resetMargin is added to the announced reset before sending into an empty
// bucket again. Discord does not always honour its own Reset-After to the
// millisecond: on a bucket of one request announcing five seconds, a request
// sent 5.001 s later was refused with 0.3 s more to wait. Client libraries
// add a quarter of a second for the same reason.
func resetMargin(reset time.Duration) time.Duration {
	return max(500*time.Millisecond, reset/10)
}

// observe reads the rate limit headers into the result and the bucket state.
func (c *client) observe(r *Result, h http.Header) {
	r.Bucket = h.Get("X-RateLimit-Bucket")
	r.Scope = h.Get("X-RateLimit-Scope")
	r.Global = h.Get("X-RateLimit-Global") == "true"
	r.Limit, _ = strconv.Atoi(h.Get("X-RateLimit-Limit"))
	r.Remaining = -1
	if v, err := strconv.Atoi(h.Get("X-RateLimit-Remaining")); err == nil {
		r.Remaining = v
	}
	r.ResetAfter, _ = strconv.ParseFloat(h.Get("X-RateLimit-Reset-After"), 64)
	if r.Status == http.StatusTooManyRequests {
		if retry, err := strconv.ParseFloat(h.Get("Retry-After"), 64); err == nil && retry > r.ResetAfter {
			r.ResetAfter = retry
		}
	}

	if r.Bucket == "" {
		return
	}
	id := r.Bucket + ":" + r.Major
	c.routeBuckets[r.Method+" "+r.Route+" "+r.Major] = id
	b := c.buckets[id]
	if b == nil {
		b = &bucketState{}
		c.buckets[id] = b
	}
	b.remaining = r.Remaining
	if r.Status == http.StatusTooManyRequests {
		b.remaining = 0
	}
	b.window = time.Duration(r.ResetAfter * float64(time.Second))
	b.resetAt = r.At.Add(b.window)
}

// majorOf extracts the major parameter from a path, given its template: the
// channel, guild or webhook it acts on, and for a webhook its token too, since
// Discord counts each token of one webhook apart. The token only appears as a
// short digest: reports get shared, and a webhook token is a credential.
func majorOf(route, path string) string {
	rParts := strings.Split(strings.Trim(route, "/"), "/")
	pParts := strings.Split(strings.Trim(strings.SplitN(path, "?", 2)[0], "/"), "/")
	if len(rParts) != len(pParts) {
		return ""
	}
	for i, part := range rParts {
		switch part {
		case "{channel_id}", "{guild_id}":
			return pParts[i]
		case "{webhook_id}":
			if i+1 < len(rParts) && rParts[i+1] == "{webhook_token}" {
				sum := sha256.Sum256([]byte(pParts[i+1]))
				return pParts[i] + "/" + hex.EncodeToString(sum[:4])
			}
			return pParts[i]
		}
	}
	return ""
}

// redact hides the tokens of a path, for error messages that end up in the
// report.
func redact(route, path string) string {
	rParts := strings.Split(strings.Trim(route, "/"), "/")
	pParts := strings.Split(strings.Trim(strings.SplitN(path, "?", 2)[0], "/"), "/")
	if len(rParts) != len(pParts) {
		return route
	}
	for i, part := range rParts {
		if strings.HasSuffix(part, "_token}") {
			pParts[i] = part
		}
	}
	return "/" + strings.Join(pParts, "/")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
