package engine

import (
	"strings"
	"testing"
)

// A recorded webhook never carries its token, neither as a field nor inside
// its URL.
func TestRecordedBodiesHideTokens(t *testing.T) {
	answer := []byte(`{"id":"1","token":"secret-token","url":"https://discord.com/api/webhooks/1/secret-token","user":{"id":"2"}}`)
	got := string(recordResponse("application/json", answer))
	if strings.Contains(got, "secret") {
		t.Fatalf("token left in %s", got)
	}
	if !strings.Contains(got, `"id":"1"`) || !strings.Contains(got, "/webhooks/1/") {
		t.Fatalf("too much hidden: %s", got)
	}
	if got := string(recordResponse("image/png", []byte{1, 2, 3})); !strings.Contains(got, `"size":3`) {
		t.Fatalf("binary answer recorded as %s", got)
	}
}

func TestCompareShapes(t *testing.T) {
	direct := &Report{Results: []Result{{
		Step: "read", Method: "GET", Route: "/x", Status: 200,
		Response: []byte(`{"id":"1","name":"a","flags":0,"topic":null,"roles":[{"id":"1","color":0}]}`),
	}}}
	candidate := &Report{Results: []Result{{
		Step: "read", Method: "GET", Route: "/x", Status: 200,
		Response: []byte(`{"id":"1","name":"a","flags":"0","topic":"t","extra":true,"roles":[{"id":"1"}]}`),
	}}}
	got := CompareShapes(direct, candidate)
	if len(got) != 1 {
		t.Fatalf("diffs = %+v", got)
	}
	d := got[0]
	if strings.Join(d.Missing, ",") != "roles[].color" || strings.Join(d.Extra, ",") != "extra" || len(d.Types) != 1 || !strings.HasPrefix(d.Types[0], "flags") {
		t.Fatalf("diff = %+v", d)
	}
	// Identical answers, or a null on one side, are not differences.
	if got := CompareShapes(direct, direct); len(got) != 0 {
		t.Fatalf("a run differs from itself: %+v", got)
	}
}

// Maps keyed by ids compare by the shape of their values, not their keys.
func TestCompareShapesTreatsIdKeyedObjectsAsMaps(t *testing.T) {
	direct := &Report{Results: []Result{{Step: "c", Method: "GET", Route: "/c", Status: 200, Response: []byte(`{"111":2,"222":0}`)}}}
	candidate := &Report{Results: []Result{{Step: "c", Method: "GET", Route: "/c", Status: 200, Response: []byte(`{"333":1}`)}}}
	if got := CompareShapes(direct, candidate); len(got) != 0 {
		t.Fatalf("id keys reported as fields: %+v", got)
	}
}

// A list holding several kinds is compared by the union of their fields, not
// by whichever element happens to come first.
func TestCompareShapesUnitesListElements(t *testing.T) {
	direct := &Report{Results: []Result{{Step: "l", Method: "GET", Route: "/l", Status: 200, Response: []byte(`[{"id":"1","type":4},{"id":"2","type":0,"topic":null}]`)}}}
	candidate := &Report{Results: []Result{{Step: "l", Method: "GET", Route: "/l", Status: 200, Response: []byte(`[{"id":"2","type":0,"topic":null},{"id":"1","type":4}]`)}}}
	if got := CompareShapes(direct, candidate); len(got) != 0 {
		t.Fatalf("order reported as a difference: %+v", got)
	}
}
