package engine

import (
	"math"
	"testing"
	"time"
)

// sequence builds the results of consecutive requests on one bucket, at the
// given offsets, with the remaining and reset-after the bucket answered.
func sequence(route string, limit int, offsets, resets []float64, remaining []int) []Result {
	start := time.Unix(1_800_000_000, 0)
	out := make([]Result, len(offsets))
	for i := range offsets {
		out[i] = Result{
			Method: "PATCH", Route: route, Bucket: route, Limit: limit,
			Remaining: remaining[i], ResetAfter: resets[i],
			At: start.Add(time.Duration(offsets[i] * float64(time.Second))),
		}
	}
	return out
}

// The sequences are synthetic, built the way Discord answers: on a token
// bucket with one token back every p seconds, reset-after is the time until
// the bucket is full again, so with k tokens missing at time t it reads k*p
// minus what already came back.
func TestModelBuckets(t *testing.T) {
	cases := []struct {
		name    string
		results []Result
		model   string
		window  float64
	}{
		{
			"token bucket, 4 tokens, one back every 3s",
			sequence("/a", 4, []float64{0, 0.2, 0.4, 0.6}, []float64{3, 5.8, 8.6, 11.4}, []int{3, 2, 1, 0}),
			"token bucket", 12,
		},
		{
			"fixed window, 10 per minute",
			sequence("/b", 10, []float64{0, 5}, []float64{60, 55}, []int{9, 8}),
			"fixed window", 60,
		},
		{
			// A run can start on a bucket already in use, with no request that
			// opened it: the step between two requests stands in for a token.
			"token bucket joined half way through",
			sequence("/c", 100, []float64{0, 0.25, 0.5}, []float64{120, 149.75, 179.5}, []int{96, 95, 94}),
			"token bucket", 3000,
		},
		{
			"fixed window joined half way through",
			sequence("/d", 10, []float64{0, 0.25}, []float64{40, 39.75}, []int{7, 6}),
			"fixed window", 40,
		},
		{
			"slow token bucket, requests far apart",
			sequence("/e", 100, []float64{0, 10}, []float64{30, 50}, []int{99, 98}),
			"token bucket", 3000,
		},
	}
	for _, c := range cases {
		got := modelBuckets(c.results)
		if len(got) != 1 {
			t.Fatalf("%s: %d buckets, want 1", c.name, len(got))
		}
		if got[0].Model != c.model {
			t.Errorf("%s: model %q, want %q", c.name, got[0].Model, c.model)
		}
		if math.Abs(got[0].WindowSeconds-c.window)/c.window > 0.01 {
			t.Errorf("%s: window %.1fs, want %.1fs", c.name, got[0].WindowSeconds, c.window)
		}
	}
}

// A route Discord does not limit on its own answers a limit of a thousand that
// resets within a millisecond.
func TestModelBucketsRecognisesGlobalOnlyRoutes(t *testing.T) {
	got := modelBuckets(sequence("/f", 1000, []float64{0, 0.2}, []float64{0.001, 0.001}, []int{999, 999}))
	if len(got) != 1 || got[0].Model != "global only" {
		t.Fatalf("got %+v, want one global only bucket", got)
	}
}

// A single request that opened a bucket says how long until it comes back,
// not whether the bucket is a window or a token bucket.
func TestModelBucketsKeepsWhatIsSureOfAnUnknownBucket(t *testing.T) {
	got := modelBuckets(sequence("/g", 1000, []float64{0}, []float64{86.4}, []int{999}))
	if len(got) != 1 || got[0].Model != "unknown" || got[0].FirstBackSeconds != 86.4 {
		t.Fatalf("got %+v", got)
	}
}
