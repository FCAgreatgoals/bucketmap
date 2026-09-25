package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// administrator is the ADMINISTRATOR permission bit.
const administrator = 1 << 3

type object struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
	Code  string `json:"code"`
}

type scenario struct {
	c   *client
	cfg Config

	botID, appID  string
	guildName     string
	community     bool
	rejoinChannel string

	roles    []string
	category string
	text     string
	text2    string
	voice    string
	forum    string
	threads  []string
	messages []string
	webhook  object
	webhook2 object
	stickers []string
	events   []string
	appEmoji string
	template string

	failures []string
	skipped  []string
}

// errSkip marks a step that could not run, because an earlier one failed or
// the guild lacks what it needs.
type errSkip struct{ reason string }

func (e errSkip) Error() string { return e.reason }

func skip(reason string) error { return errSkip{reason} }

// afterFailure is the reason given when a step lacks what an earlier one
// should have created.
var afterFailure = skip("an earlier step failed")

// step runs one scenario step and records its outcome. Steps keep going after
// a failure: one broken route should not hide the state of all the others.
func (s *scenario) step(name string, fn func() error) {
	err := fn()
	var sk errSkip
	switch {
	case err == nil:
	case errors.As(err, &sk):
		s.skipped = append(s.skipped, name+": "+sk.reason)
	default:
		s.failures = append(s.failures, fmt.Sprintf("%s: %v", name, err))
	}
}

// optional runs a step whose route may not be available on every guild or
// every application: a 400, 403 or 404 skips it with the reason instead of
// failing the run. The request is still recorded, and still compared.
func (s *scenario) optional(name string, fn func() error) {
	s.step(name, func() error {
		err := fn()
		var se *statusError
		if errors.As(err, &se) && (se.status == 400 || se.status == 403 || se.status == 404) {
			return skip(fmt.Sprintf("not available here (%d)", se.status))
		}
		return err
	})
}

// communityOnly runs a step only on a guild with the COMMUNITY feature.
func (s *scenario) communityOnly(name string, fn func() error) {
	s.step(name, func() error {
		if !s.community {
			return skip("needs a community guild")
		}
		return fn()
	})
}

func (s *scenario) need(values ...string) error {
	for _, v := range values {
		if v == "" {
			return afterFailure
		}
	}
	return nil
}

// do sends one request. A read that succeeds is sent a second time at once:
// two requests in a row on one bucket are what tells its model, and reading
// twice changes nothing.
func (s *scenario) do(step, method, route, path string, body, out any) error {
	if err := s.c.do(call{step: step, method: method, route: route, path: path, body: body}, out); err != nil {
		return err
	}
	if method != "GET" {
		return nil
	}
	return s.c.do(call{step: step + " (again)", method: method, route: route, path: path, immediate: true}, nil)
}

// pair sends the same request twice in a row, the second without spacing.
// Two consecutive requests on one bucket are what tells a token bucket from a
// fixed window, and two requests stay far below any limit.
func (s *scenario) pair(step, method, route, path string, body any) error {
	if err := s.c.do(call{step: step + " (1/2)", method: method, route: route, path: path, body: body}, nil); err != nil {
		return err
	}
	return s.c.do(call{step: step + " (2/2)", method: method, route: route, path: path, body: body, immediate: true}, nil)
}

func (s *scenario) g() string { return "/guilds/" + s.cfg.Guild }

// group is a set of steps that can run on its own, after the preflight and
// the setup, as long as the groups it needs run too.
type group struct {
	name  string
	needs []string
	run   func(*scenario)
}

// groups is the scenario, in the order it runs.
var groups = []group{
	{"messages", nil, (*scenario).exerciseMessages},
	{"reactions", []string{"messages"}, (*scenario).exerciseReactions},
	{"pins", []string{"messages"}, (*scenario).exercisePins},
	{"threads", []string{"messages"}, (*scenario).exerciseThreads},
	{"attachments", nil, (*scenario).exerciseAttachments},
	{"polls", nil, (*scenario).exercisePolls},
	{"channel", nil, (*scenario).exerciseChannel},
	{"invites", nil, (*scenario).exerciseInvites},
	{"webhooks", nil, (*scenario).exerciseWebhooks},
	{"guild", nil, (*scenario).exerciseGuild},
	{"guild-reads", nil, (*scenario).exerciseGuildReads},
	{"roles", nil, (*scenario).exerciseRoles},
	{"members", nil, (*scenario).exerciseMembers},
	{"automod", nil, (*scenario).exerciseAutomod},
	{"emojis", nil, (*scenario).exerciseEmojis},
	{"stickers", nil, (*scenario).exerciseStickers},
	{"soundboard", nil, (*scenario).exerciseSoundboard},
	{"events", nil, (*scenario).exerciseEvents},
	{"templates", nil, (*scenario).exerciseTemplates},
	{"community", nil, (*scenario).exerciseCommunity},
	{"application", nil, (*scenario).exerciseApplication},
	{"commands", nil, (*scenario).exerciseCommands},
	{"users", nil, (*scenario).exerciseUsers},
	{"public", nil, (*scenario).exercisePublic},
	{"moderation", nil, (*scenario).moderate},
}

// Groups lists the names a run can be restricted to.
func Groups() []string {
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = g.name
	}
	return out
}

// selected resolves the groups to run: every group when only is empty,
// otherwise those named and the groups they need.
func selected(only []string) (map[string]bool, error) {
	if len(only) == 0 {
		all := map[string]bool{}
		for _, g := range groups {
			all[g.name] = true
		}
		return all, nil
	}
	byName := map[string]group{}
	for _, g := range groups {
		byName[g.name] = g
	}
	out := map[string]bool{}
	var add func(name string) error
	add = func(name string) error {
		g, ok := byName[name]
		if !ok {
			return fmt.Errorf("unknown group %q, known groups: %s", name, strings.Join(Groups(), ", "))
		}
		if out[name] {
			return nil
		}
		out[name] = true
		for _, n := range g.needs {
			if err := add(n); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range only {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *scenario) run() error {
	run, err := selected(s.cfg.Only)
	if err != nil {
		return err
	}
	s.c.group = "preflight"
	if err := s.preflight(); err != nil {
		return err
	}
	defer func() {
		s.c.group = "cleanup"
		s.cleanup()
	}()

	s.c.group = "setup"
	s.setup()
	for _, g := range groups {
		if run[g.name] {
			s.c.group = g.name
			g.run(s)
		}
	}
	return nil
}

// preflight checks the guild is a test guild the bot administers, and refuses
// to go further otherwise. It is the only step that can stop the run.
func (s *scenario) preflight() error {
	var me object
	if err := s.do("identify the bot", "GET", "/users/@me", "/users/@me", nil, &me); err != nil {
		return err
	}
	s.botID = me.ID
	var app object
	if err := s.do("identify the application", "GET", "/applications/@me", "/applications/@me", nil, &app); err != nil {
		return err
	}
	s.appID = app.ID
	s.step("read gateway", func() error { return s.do("read gateway", "GET", "/gateway/bot", "/gateway/bot", nil, nil) })

	var guild struct {
		Name     string   `json:"name"`
		Features []string `json:"features"`
	}
	if err := s.do("read guild", "GET", "/guilds/{guild_id}", s.g(), nil, &guild); err != nil {
		return err
	}
	if s.cfg.Marker == "" || !strings.Contains(guild.Name, s.cfg.Marker) {
		return fmt.Errorf("guild %q does not carry the marker %q in its name: refusing to touch it", guild.Name, s.cfg.Marker)
	}
	s.guildName = guild.Name
	for _, f := range guild.Features {
		if f == "COMMUNITY" {
			s.community = true
		}
	}

	var roles []struct {
		ID          string `json:"id"`
		Permissions string `json:"permissions"`
	}
	if err := s.do("read roles", "GET", "/guilds/{guild_id}/roles", s.g()+"/roles", nil, &roles); err != nil {
		return err
	}
	var bot struct {
		Roles []string `json:"roles"`
	}
	if err := s.do("read bot member", "GET", "/guilds/{guild_id}/members/{user_id}", s.g()+"/members/"+s.botID, nil, &bot); err != nil {
		return err
	}
	if !hasAdministrator(roles, append(bot.Roles, s.cfg.Guild)) {
		return errors.New("the bot is not an administrator of the guild")
	}

	for i, user := range s.cfg.Users {
		name := fmt.Sprintf("read test user %d", i+1)
		if err := s.do(name, "GET", "/guilds/{guild_id}/members/{user_id}", s.g()+"/members/"+user, nil, nil); err != nil {
			return fmt.Errorf("test user %d (%s) is not a member of the guild: %w", i+1, user, err)
		}
	}

	var channels []struct {
		ID   string `json:"id"`
		Type int    `json:"type"`
	}
	if err := s.do("list channels", "GET", "/guilds/{guild_id}/channels", s.g()+"/channels", nil, &channels); err != nil {
		return err
	}
	for _, ch := range channels {
		if ch.Type == 0 {
			// A channel the engine did not create, so the invite it prints for
			// expelled test users survives the cleanup.
			s.rejoinChannel = ch.ID
			break
		}
	}
	return nil
}

func hasAdministrator(roles []struct {
	ID          string `json:"id"`
	Permissions string `json:"permissions"`
}, held []string) bool {
	for _, role := range roles {
		for _, id := range held {
			if role.ID != id {
				continue
			}
			if perms, err := strconv.ParseUint(role.Permissions, 10, 64); err == nil && perms&administrator != 0 {
				return true
			}
		}
	}
	return false
}

// setup builds what the scenario works on: roles, a category, channels and a
// permission overwrite. Everything is named after the marker.
func (s *scenario) setup() {
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("create role %d", i)
		s.step(name, func() error {
			var role object
			err := s.do(name, "POST", "/guilds/{guild_id}/roles", s.g()+"/roles", map[string]any{"name": fmt.Sprintf("%s-role-%d", s.cfg.Marker, i)}, &role)
			if err == nil {
				s.roles = append(s.roles, role.ID)
			}
			return err
		})
	}
	s.step("edit role", func() error {
		if len(s.roles) == 0 {
			return afterFailure
		}
		return s.do("edit role", "PATCH", "/guilds/{guild_id}/roles/{role_id}", s.g()+"/roles/"+s.roles[0], map[string]any{"mentionable": true}, nil)
	})

	create := func(step string, body map[string]any, into *string) {
		s.step(step, func() error {
			var ch object
			err := s.do(step, "POST", "/guilds/{guild_id}/channels", s.g()+"/channels", body, &ch)
			*into = ch.ID
			return err
		})
	}
	m := s.cfg.Marker
	create("create category", map[string]any{"name": m, "type": 4}, &s.category)
	create("create text channel", map[string]any{"name": m + "-text", "type": 0, "parent_id": nilIfEmpty(s.category)}, &s.text)
	create("create second text channel", map[string]any{"name": m + "-text-2", "type": 0, "parent_id": nilIfEmpty(s.category)}, &s.text2)
	create("create voice channel", map[string]any{"name": m + "-voice", "type": 2, "parent_id": nilIfEmpty(s.category)}, &s.voice)
	s.optional("create forum channel", func() error {
		var ch object
		err := s.do("create forum channel", "POST", "/guilds/{guild_id}/channels", s.g()+"/channels", map[string]any{"name": m + "-forum", "type": 15, "parent_id": nilIfEmpty(s.category)}, &ch)
		s.forum = ch.ID
		return err
	})

	s.step("set permission overwrite", func() error {
		if len(s.roles) == 0 || s.text2 == "" {
			return afterFailure
		}
		return s.do("set permission overwrite", "PUT", "/channels/{channel_id}/permissions/{overwrite_id}", "/channels/"+s.text2+"/permissions/"+s.roles[0], map[string]any{"type": 0, "allow": "1024", "deny": "0"}, nil)
	})
	s.step("remove permission overwrite", func() error {
		if len(s.roles) == 0 || s.text2 == "" {
			return afterFailure
		}
		return s.do("remove permission overwrite", "DELETE", "/channels/{channel_id}/permissions/{overwrite_id}", "/channels/"+s.text2+"/permissions/"+s.roles[0], nil, nil)
	})
}

func nilIfEmpty(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// cleanup removes everything the scenario created, even after failures.
func (s *scenario) cleanup() {
	for i, w := range []object{s.webhook, s.webhook2} {
		if w.ID == "" {
			continue
		}
		name := fmt.Sprintf("delete webhook %d", i+1)
		s.step(name, func() error {
			return s.do(name, "DELETE", "/webhooks/{webhook_id}", "/webhooks/"+w.ID, nil, nil)
		})
	}
	for i, id := range s.stickers {
		name := fmt.Sprintf("delete sticker %d", i+1)
		s.step(name, func() error {
			return s.do(name, "DELETE", "/guilds/{guild_id}/stickers/{sticker_id}", s.g()+"/stickers/"+id, nil, nil)
		})
	}
	for i, id := range s.events {
		name := fmt.Sprintf("delete scheduled event %d", i+1)
		s.step(name, func() error {
			return s.do(name, "DELETE", "/guilds/{guild_id}/scheduled-events/{guild_scheduled_event_id}", s.g()+"/scheduled-events/"+id, nil, nil)
		})
	}
	if s.template != "" {
		s.step("delete template", func() error {
			return s.do("delete template", "DELETE", "/guilds/{guild_id}/templates/{code}", s.g()+"/templates/"+s.template, nil, nil)
		})
	}
	if s.appEmoji != "" {
		s.step("delete application emoji", func() error {
			return s.do("delete application emoji", "DELETE", "/applications/{application_id}/emojis/{emoji_id}", "/applications/"+s.appID+"/emojis/"+s.appEmoji, nil, nil)
		})
	}
	for i, id := range append(append([]string{}, s.threads...), s.text, s.text2, s.voice, s.forum, s.category) {
		if id == "" {
			continue
		}
		name := fmt.Sprintf("delete channel %d", i+1)
		s.step(name, func() error {
			return s.do(name, "DELETE", "/channels/{channel_id}", "/channels/"+id, nil, nil)
		})
	}
	for i, id := range s.roles {
		name := fmt.Sprintf("delete role %d", i+1)
		s.step(name, func() error {
			return s.do(name, "DELETE", "/guilds/{guild_id}/roles/{role_id}", s.g()+"/roles/"+id, nil, nil)
		})
	}
}

// rejoinInvite creates an invite on a channel the engine did not create, for
// test users it kicked or banned to come back.
func (s *scenario) rejoinInvite() string {
	if s.rejoinChannel == "" || (!s.cfg.AllowKick && !s.cfg.AllowBan) {
		return ""
	}
	var invite object
	if err := s.do("create rejoin invite", "POST", "/channels/{channel_id}/invites", "/channels/"+s.rejoinChannel+"/invites", map[string]any{"max_age": 86400, "max_uses": 0}, &invite); err != nil {
		s.failures = append(s.failures, "create rejoin invite: "+err.Error())
		return ""
	}
	return "https://discord.gg/" + invite.Code
}
