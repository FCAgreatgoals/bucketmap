package routes

import "testing"

func TestIndexLoadsWithoutDuplicates(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 300 {
		t.Fatalf("%d routes, the index looks truncated", len(all))
	}
	seen := map[string]bool{}
	for _, r := range all {
		k := r.Method + " " + r.Path
		if seen[k] {
			t.Errorf("%s is indexed twice", k)
		}
		seen[k] = true
		if r.Coverage == "" || r.Model == "" {
			t.Errorf("%s has no coverage or model", k)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct{ method, path, want, major string }{
		{"POST", "/api/v10/channels/111/messages?foo=bar", "/channels/{channel_id}/messages", "111"},
		{"PATCH", "/guilds/222/members/@me", "/guilds/{guild_id}/members/@me", "222"},
		{"PATCH", "/guilds/222/members/333", "/guilds/{guild_id}/members/{user_id}", "222"},
		{"GET", "/api/users/@me", "/users/@me", ""},
		{"POST", "/webhooks/444/tok", "/webhooks/{webhook_id}/{webhook_token}", "444/tok"},
		{"GET", "/applications/555/guilds/222/commands", "/applications/{application_id}/guilds/{guild_id}/commands", "222"},
	}
	for _, c := range cases {
		r, ok := Match(c.method, c.path)
		if !ok || r.Path != c.want {
			t.Errorf("%s %s: matched %q, want %q", c.method, c.path, r.Path, c.want)
			continue
		}
		if got := r.MajorValue(c.path); got != c.major {
			t.Errorf("%s %s: major %q, want %q", c.method, c.path, got, c.major)
		}
	}
	if _, ok := Match("GET", "/nothing/here"); ok {
		t.Error("an unknown path matched")
	}
}

func TestInteractionCallbackIsExemptFromTheGlobalLimit(t *testing.T) {
	r, ok := Match("POST", "/interactions/1/tok/callback")
	if !ok || r.Global {
		t.Fatalf("callback: matched %v, global %v", ok, r.Global)
	}
}
