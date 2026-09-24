package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FCAgreatgoals/bucketmap/routes"
)

// The full dry run walks every step: a step that fails or skips there has a
// bug of its own, since the responder answers everything with success.
func TestDryRunWalksEveryStep(t *testing.T) {
	r := DryRun(true)
	for _, f := range r.Failures {
		t.Errorf("failed: %s", f)
	}
	for _, s := range r.Skipped {
		t.Errorf("skipped: %s", s)
	}
}

// Without flags and on a guild that is not a community guild, only the steps
// that need one or the other are left out.
func TestPlainDryRunOnlySkipsCommunitySteps(t *testing.T) {
	r := DryRun(false)
	for _, f := range r.Failures {
		t.Errorf("failed: %s", f)
	}
	for _, s := range r.Skipped {
		if !strings.HasSuffix(s, "needs a community guild") && !strings.Contains(s, "no stage channel") {
			t.Errorf("skipped: %s", s)
		}
	}
}

// Every route the scenario calls is in the index under the same name, and the
// index says so: rebuild it with `bucketmap index` when this fails.
func TestIndexKnowsWhatTheScenarioCovers(t *testing.T) {
	all, err := routes.All()
	if err != nil {
		t.Fatal(err)
	}
	coverage := map[string]routes.Coverage{}
	for _, r := range all {
		coverage[r.Method+" "+r.Path] = r.Coverage
	}
	plain := map[string]bool{}
	for _, name := range Covered(DryRun(false)) {
		plain[name] = true
	}
	for _, name := range Covered(DryRun(true)) {
		want := routes.Gated
		if plain[name] {
			want = routes.Exercised
		}
		got, ok := coverage[name]
		switch {
		case !ok:
			t.Errorf("%s is called by the scenario but missing from the index", name)
		case got != want:
			t.Errorf("%s: index says %q, the scenario makes it %q", name, got, want)
		}
	}
}

// Webhook tokens are credentials, and reports get shared.
func TestReportsNeverCarryWebhookTokens(t *testing.T) {
	raw, err := json.Marshal(DryRun(true))
	if err != nil {
		t.Fatal(err)
	}
	if majorOf("/webhooks/{webhook_id}/{webhook_token}", "/webhooks/1/secret-token") == "1/secret-token" {
		t.Error("the major parameter carries the webhook token")
	}
	if got := redact("/webhooks/{webhook_id}/{webhook_token}", "/webhooks/1/secret-token?wait=true"); strings.Contains(got, "secret") {
		t.Errorf("redact left the token: %s", got)
	}
	if strings.Contains(string(raw), `"major":"1/t"`) {
		t.Error("a report carries a webhook token")
	}
}
