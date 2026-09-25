package engine

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"time"
)

// tinyPNG is a 1x1 transparent PNG, enough to create an emoji.
const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// stickerPNG is a plain 320x320 PNG, the size Discord requires of a sticker.
func stickerPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 320, 320))
	for y := 0; y < 320; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{88, 101, 242, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// silentMP3 is about a second of silence: MPEG-1 layer III frames at 128 kbit/s
// and 44.1 kHz whose side information is all zeros.
func silentMP3() []byte {
	frame := make([]byte, 417)
	copy(frame, []byte{0xFF, 0xFB, 0x90, 0x64})
	return bytes.Repeat(frame, 38)
}

// exerciseGuild edits the guild in ways that leave it as it was.
func (s *scenario) exerciseGuild() {
	s.step("edit guild", func() error {
		return s.do("edit guild", "PATCH", "/guilds/{guild_id}", s.g(), map[string]any{"name": s.guildName}, nil)
	})

	var widget struct {
		Enabled   bool    `json:"enabled"`
		ChannelID *string `json:"channel_id"`
	}
	s.step("read widget settings", func() error {
		return s.do("read widget settings", "GET", "/guilds/{guild_id}/widget", s.g()+"/widget", nil, &widget)
	})
	s.step("widget on and off", func() error {
		if err := s.do("enable widget", "PATCH", "/guilds/{guild_id}/widget", s.g()+"/widget", map[string]any{"enabled": true}, nil); err != nil {
			return err
		}
		if err := s.do("read widget", "GET", "/guilds/{guild_id}/widget.json", s.g()+"/widget.json", nil, nil); err != nil {
			return err
		}
		if err := s.do("read widget image", "GET", "/guilds/{guild_id}/widget.png", s.g()+"/widget.png?style=shield", nil, nil); err != nil {
			return err
		}
		return s.do("restore widget", "PATCH", "/guilds/{guild_id}/widget", s.g()+"/widget", map[string]any{"enabled": widget.Enabled}, nil)
	})

	// The raid lockdown: invites and direct messages paused, then resumed.
	s.step("pause then resume invites and DMs", func() error {
		route := "/guilds/{guild_id}/incident-actions"
		until := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
		if err := s.do("pause invites and DMs", "PUT", route, s.g()+"/incident-actions", map[string]any{"invites_disabled_until": until, "dms_disabled_until": until}, nil); err != nil {
			return err
		}
		return s.do("resume invites and DMs", "PUT", route, s.g()+"/incident-actions", map[string]any{"invites_disabled_until": nil, "dms_disabled_until": nil}, nil)
	})
}

func (s *scenario) exerciseGuildReads() {
	g := s.g()
	reads := []struct{ step, route, path string }{
		{"list members", "/guilds/{guild_id}/members", g + "/members?limit=10"},
		{"search members", "/guilds/{guild_id}/members/search", g + "/members/search?query=" + url.QueryEscape("t") + "&limit=5"},
		{"list bans", "/guilds/{guild_id}/bans", g + "/bans?limit=10"},
		{"read audit log", "/guilds/{guild_id}/audit-logs", g + "/audit-logs?limit=5"},
		{"read onboarding", "/guilds/{guild_id}/onboarding", g + "/onboarding"},
		{"list roles", "/guilds/{guild_id}/roles", g + "/roles"},
		{"read guild preview", "/guilds/{guild_id}/preview", g + "/preview"},
		{"list voice regions of the guild", "/guilds/{guild_id}/regions", g + "/regions"},
		{"list integrations", "/guilds/{guild_id}/integrations", g + "/integrations"},
		{"count prunable members", "/guilds/{guild_id}/prune", g + "/prune?days=30"},
		{"list role member counts", "/guilds/{guild_id}/roles/member-counts", g + "/roles/member-counts"},
	}
	for _, r := range reads {
		s.step(r.step, func() error { return s.do(r.step, "GET", r.route, r.path, nil, nil) })
	}

	// Reads that depend on the guild or on what the application may do.
	optional := []struct{ step, route, path string }{
		{"read vanity URL", "/guilds/{guild_id}/vanity-url", g + "/vanity-url"},
		{"read basic guild", "/guilds/{guild_id}/basic", g + "/basic"},
		{"list top read channels", "/guilds/{guild_id}/top-read-channels", g + "/top-read-channels"},
		{"search guild messages", "/guilds/{guild_id}/messages/search", g + "/messages/search?content=bucketmap&limit=5"},
		{"list join requests", "/guilds/{guild_id}/requests", g + "/requests?limit=5"},
		{"read own voice state", "/guilds/{guild_id}/voice-states/@me", g + "/voice-states/@me"},
	}
	for _, r := range optional {
		s.optional(r.step, func() error { return s.do(r.step, "GET", r.route, r.path, nil, nil) })
	}
	s.optional("read a member's voice state", func() error {
		if len(s.cfg.Users) == 0 {
			return afterFailure
		}
		return s.do("read a member's voice state", "GET", "/guilds/{guild_id}/voice-states/{user_id}", g+"/voice-states/"+s.cfg.Users[0], nil, nil)
	})
	s.optional("search members, newer route", func() error {
		return s.do("search members, newer route", "POST", "/guilds/{guild_id}/members-search", g+"/members-search", map[string]any{"limit": 5}, nil)
	})

	// Two different users back to back: whether the second one's remaining
	// follows the first tells if every user shares one counter.
	s.step("read two users", func() error {
		if len(s.cfg.Users) < 2 {
			return afterFailure
		}
		if err := s.do("read user", "GET", "/users/{user_id}", "/users/"+s.cfg.Users[0], nil, nil); err != nil {
			return err
		}
		return s.c.do(call{step: "read another user", method: "GET", route: "/users/{user_id}", path: "/users/" + s.cfg.Users[1], immediate: true}, nil)
	})
}

func (s *scenario) exerciseRoles() {
	s.step("read role", func() error {
		if len(s.roles) == 0 {
			return afterFailure
		}
		return s.do("read role", "GET", "/guilds/{guild_id}/roles/{role_id}", s.g()+"/roles/"+s.roles[0], nil, nil)
	})
	s.step("reorder roles", func() error {
		if len(s.roles) < 3 {
			return afterFailure
		}
		var roles []struct {
			ID       string `json:"id"`
			Position int    `json:"position"`
		}
		if err := s.do("read role positions", "GET", "/guilds/{guild_id}/roles", s.g()+"/roles", nil, &roles); err != nil {
			return err
		}
		pos := map[string]int{}
		for _, r := range roles {
			pos[r.ID] = r.Position
		}
		a, b := s.roles[1], s.roles[2]
		swap := []map[string]any{{"id": a, "position": pos[b]}, {"id": b, "position": pos[a]}}
		back := []map[string]any{{"id": a, "position": pos[a]}, {"id": b, "position": pos[b]}}
		if err := s.do("reorder roles", "PATCH", "/guilds/{guild_id}/roles", s.g()+"/roles", swap, nil); err != nil {
			return err
		}
		return s.do("restore role order", "PATCH", "/guilds/{guild_id}/roles", s.g()+"/roles", back, nil)
	})
}

// exerciseMembers adds then removes a role on every test user back to back:
// the two share one bucket, which the pair makes visible.
func (s *scenario) exerciseMembers() {
	g := s.g() + "/members/"
	for i, user := range s.cfg.Users {
		name := fmt.Sprintf("toggle role on test user %d", i+1)
		s.step(name, func() error {
			if len(s.roles) == 0 {
				return afterFailure
			}
			path := g + user + "/roles/" + s.roles[0]
			route := "/guilds/{guild_id}/members/{user_id}/roles/{role_id}"
			if err := s.do(name+" (add)", "PUT", route, path, nil, nil); err != nil {
				return err
			}
			return s.c.do(call{step: name + " (remove)", method: "DELETE", route: route, path: path, immediate: true}, nil)
		})
	}
	s.step("nickname test user 1", func() error {
		if len(s.cfg.Users) == 0 {
			return afterFailure
		}
		route := "/guilds/{guild_id}/members/{user_id}"
		if err := s.do("set nickname", "PATCH", route, g+s.cfg.Users[0], map[string]any{"nick": s.cfg.Marker}, nil); err != nil {
			return err
		}
		return s.do("clear nickname", "PATCH", route, g+s.cfg.Users[0], map[string]any{"nick": nil}, nil)
	})
	s.step("nickname the bot", func() error {
		route := "/guilds/{guild_id}/members/@me"
		if err := s.do("set bot nickname", "PATCH", route, g+"@me", map[string]any{"nick": s.cfg.Marker}, nil); err != nil {
			return err
		}
		return s.do("clear bot nickname", "PATCH", route, g+"@me", map[string]any{"nick": nil}, nil)
	})
	s.optional("nickname the bot, older route", func() error {
		route := "/guilds/{guild_id}/members/@me/nick"
		if err := s.do("set bot nickname, older route", "PATCH", route, g+"@me/nick", map[string]any{"nick": s.cfg.Marker}, nil); err != nil {
			return err
		}
		return s.do("clear bot nickname, older route", "PATCH", route, g+"@me/nick", map[string]any{"nick": nil}, nil)
	})
}

func (s *scenario) exerciseAutomod() {
	base := s.g() + "/auto-moderation/rules"
	var rule, rule2 object
	s.step("create automod rule", func() error {
		body := map[string]any{
			"name": s.cfg.Marker, "event_type": 1, "trigger_type": 1, "enabled": false,
			"trigger_metadata": map[string]any{"keyword_filter": []string{"bucketmapkeyword"}},
			"actions":          []map[string]any{{"type": 1}},
		}
		if err := s.do("create automod rule", "POST", "/guilds/{guild_id}/auto-moderation/rules", base, body, &rule); err != nil {
			return err
		}
		return s.twin("create automod rule", "/guilds/{guild_id}/auto-moderation/rules", base, body, "-twin", &rule2)
	})
	s.step("list automod rules", func() error {
		return s.do("list automod rules", "GET", "/guilds/{guild_id}/auto-moderation/rules", base, nil, nil)
	})
	one := "/guilds/{guild_id}/auto-moderation/rules/{rule_id}"
	s.step("read automod rule", func() error {
		if err := s.need(rule.ID); err != nil {
			return err
		}
		return s.do("read automod rule", "GET", one, base+"/"+rule.ID, nil, nil)
	})
	s.step("edit automod rule", func() error {
		if err := s.need(rule.ID); err != nil {
			return err
		}
		return s.do("edit automod rule", "PATCH", one, base+"/"+rule.ID, map[string]any{"name": s.cfg.Marker + "-edited"}, nil)
	})
	s.step("delete automod rule", func() error {
		if err := s.need(rule.ID); err != nil {
			return err
		}
		if err := s.do("delete automod rule", "DELETE", one, base+"/"+rule.ID, nil, nil); err != nil {
			return err
		}
		if rule2.ID != "" {
			s.dropTwin("delete automod rule", one, base+"/"+rule2.ID)
		}
		return nil
	})
}

// exerciseEmojis covers the routes Discord warns about: emoji routes are
// limited per guild outside the usual conventions, and their headers may be
// inaccurate.
func (s *scenario) exerciseEmojis() {
	base := s.g() + "/emojis"
	var emoji object
	s.step("create emoji", func() error {
		return s.do("create emoji", "POST", "/guilds/{guild_id}/emojis", base, map[string]any{"name": "bucketmap", "image": tinyPNG}, &emoji)
	})
	s.step("list emojis", func() error { return s.do("list emojis", "GET", "/guilds/{guild_id}/emojis", base, nil, nil) })
	one := "/guilds/{guild_id}/emojis/{emoji_id}"
	s.step("read emoji", func() error {
		if err := s.need(emoji.ID); err != nil {
			return err
		}
		return s.do("read emoji", "GET", one, base+"/"+emoji.ID, nil, nil)
	})
	s.step("rename emoji", func() error {
		if err := s.need(emoji.ID); err != nil {
			return err
		}
		return s.do("rename emoji", "PATCH", one, base+"/"+emoji.ID, map[string]any{"name": "bucketmap_2"}, nil)
	})
	s.step("delete emoji", func() error {
		if err := s.need(emoji.ID); err != nil {
			return err
		}
		return s.do("delete emoji", "DELETE", one, base+"/"+emoji.ID, nil, nil)
	})
}

func (s *scenario) exerciseStickers() {
	base := s.g() + "/stickers"
	one := "/guilds/{guild_id}/stickers/{sticker_id}"
	var sticker object
	s.optional("create sticker", func() error {
		body := multipartBody{
			fields: map[string]string{"name": "bucketmap", "description": "bucketmap sticker", "tags": "robot"},
			files:  []file{{field: "file", name: "bucketmap.png", contentType: "image/png", data: stickerPNG()}},
		}
		err := s.do("create sticker", "POST", "/guilds/{guild_id}/stickers", base, body, &sticker)
		if err == nil && sticker.ID != "" {
			s.stickers = append(s.stickers, sticker.ID)
		}
		return err
	})
	s.step("list stickers", func() error { return s.do("list stickers", "GET", "/guilds/{guild_id}/stickers", base, nil, nil) })
	s.step("read sticker", func() error {
		if sticker.ID == "" {
			return skip("no sticker was created")
		}
		if err := s.do("read sticker", "GET", one, base+"/"+sticker.ID, nil, nil); err != nil {
			return err
		}
		return s.do("read sticker anywhere", "GET", "/stickers/{sticker_id}", "/stickers/"+sticker.ID, nil, nil)
	})
	s.step("edit sticker", func() error {
		if sticker.ID == "" {
			return skip("no sticker was created")
		}
		return s.do("edit sticker", "PATCH", one, base+"/"+sticker.ID, map[string]any{"description": "bucketmap sticker, edited"}, nil)
	})
}

func (s *scenario) exerciseSoundboard() {
	s.step("list default sounds", func() error {
		return s.do("list default sounds", "GET", "/soundboard-default-sounds", "/soundboard-default-sounds", nil, nil)
	})
	base := s.g() + "/soundboard-sounds"
	one := "/guilds/{guild_id}/soundboard-sounds/{sound_id}"
	s.step("list guild sounds", func() error {
		return s.do("list guild sounds", "GET", "/guilds/{guild_id}/soundboard-sounds", base, nil, nil)
	})
	s.optional("create, edit and delete a sound", func() error {
		var sound struct {
			SoundID string `json:"sound_id"`
		}
		data := "data:audio/mpeg;base64," + base64Encode(silentMP3())
		if err := s.do("create sound", "POST", "/guilds/{guild_id}/soundboard-sounds", base, map[string]any{"name": "bucketmap", "sound": data, "volume": 0.1}, &sound); err != nil {
			return err
		}
		if sound.SoundID == "" {
			return afterFailure
		}
		if err := s.do("read sound", "GET", one, base+"/"+sound.SoundID, nil, nil); err != nil {
			return err
		}
		if err := s.do("edit sound", "PATCH", one, base+"/"+sound.SoundID, map[string]any{"name": "bucketmap_2"}, nil); err != nil {
			return err
		}
		return s.do("delete sound", "DELETE", one, base+"/"+sound.SoundID, nil, nil)
	})
}

// exerciseEvents schedules an external event an hour ahead, which nobody is
// notified of, and a weekly one to cancel a single occurrence.
func (s *scenario) exerciseEvents() {
	base := s.g() + "/scheduled-events"
	list := "/guilds/{guild_id}/scheduled-events"
	one := list + "/{guild_scheduled_event_id}"
	start := time.Now().Add(time.Hour).UTC().Truncate(time.Minute)
	var event object
	s.step("create scheduled event", func() error {
		err := s.do("create scheduled event", "POST", list, base, map[string]any{
			"name": s.cfg.Marker, "privacy_level": 2, "entity_type": 3,
			"scheduled_start_time": start.Format(time.RFC3339), "scheduled_end_time": start.Add(time.Hour).Format(time.RFC3339),
			"entity_metadata": map[string]any{"location": "bucketmap"},
		}, &event)
		if err == nil && event.ID != "" {
			s.events = append(s.events, event.ID)
		}
		return err
	})
	s.step("list scheduled events", func() error {
		return s.do("list scheduled events", "GET", list, base+"?with_user_count=true", nil, nil)
	})
	for _, r := range []struct{ step, route, suffix string }{
		{"read scheduled event", one, ""},
		{"list scheduled event users", one + "/users", "/users?limit=10"},
	} {
		s.step(r.step, func() error {
			if err := s.need(event.ID); err != nil {
				return err
			}
			return s.do(r.step, "GET", r.route, base+"/"+event.ID+r.suffix, nil, nil)
		})
	}
	s.optional("count scheduled event users", func() error {
		if err := s.need(event.ID); err != nil {
			return err
		}
		return s.do("count scheduled event users", "GET", one+"/users/counts", base+"/"+event.ID+"/users/counts", nil, nil)
	})
	s.step("edit scheduled event", func() error {
		if err := s.need(event.ID); err != nil {
			return err
		}
		return s.do("edit scheduled event", "PATCH", one, base+"/"+event.ID, map[string]any{"name": s.cfg.Marker + "-edited"}, nil)
	})

	s.optional("cancel one occurrence of a weekly event", func() error {
		var weekly object
		err := s.do("create weekly event", "POST", list, base, map[string]any{
			"name": s.cfg.Marker + "-weekly", "privacy_level": 2, "entity_type": 3,
			"scheduled_start_time": start.Format(time.RFC3339), "scheduled_end_time": start.Add(time.Hour).Format(time.RFC3339),
			"entity_metadata": map[string]any{"location": "bucketmap"},
			"recurrence_rule": map[string]any{"start": start.Format(time.RFC3339), "frequency": 2, "interval": 1, "by_weekday": []int{int(start.Weekday()+6) % 7}},
		}, &weekly)
		if err != nil {
			return err
		}
		if weekly.ID == "" {
			return afterFailure
		}
		s.events = append(s.events, weekly.ID)
		exceptions := one + "/exceptions"
		var exception struct {
			EventExceptionID string `json:"event_exception_id"`
		}
		next := start.Add(7 * 24 * time.Hour).Format(time.RFC3339)
		if err := s.do("cancel an occurrence", "POST", exceptions, base+"/"+weekly.ID+"/exceptions", map[string]any{"original_scheduled_start_time": next, "is_canceled": true}, &exception); err != nil {
			return err
		}
		if exception.EventExceptionID == "" {
			return afterFailure
		}
		ex := base + "/" + weekly.ID + "/exceptions/" + exception.EventExceptionID
		if err := s.do("list users of an occurrence", "GET", one+"/{guild_scheduled_event_exception_id}/users", base+"/"+weekly.ID+"/"+exception.EventExceptionID+"/users", nil, nil); err != nil {
			return err
		}
		// An exception must change a field of the series (180005 otherwise):
		// the occurrence is restored half an hour later than planned.
		moved := start.Add(7*24*time.Hour + 30*time.Minute)
		if err := s.do("restore the occurrence, moved", "PATCH", exceptions+"/{exception_id}", ex, map[string]any{
			"is_canceled": false, "scheduled_start_time": moved.Format(time.RFC3339), "scheduled_end_time": moved.Add(time.Hour).Format(time.RFC3339),
		}, nil); err != nil {
			return err
		}
		return s.do("drop the exception", "DELETE", exceptions+"/{exception_id}", ex, nil, nil)
	})
}

// exerciseTemplates creates the guild's template, when it has none yet: a
// guild holds a single one.
func (s *scenario) exerciseTemplates() {
	base := s.g() + "/templates"
	list := "/guilds/{guild_id}/templates"
	one := list + "/{code}"
	s.step("list templates", func() error { return s.do("list templates", "GET", list, base, nil, nil) })
	s.optional("create template", func() error {
		var t object
		err := s.do("create template", "POST", list, base, map[string]any{"name": s.cfg.Marker}, &t)
		s.template = t.Code
		return err
	})
	s.step("use the template", func() error {
		if s.template == "" {
			return skip("no template was created")
		}
		if err := s.do("read template", "GET", "/guilds/templates/{code}", "/guilds/templates/"+s.template, nil, nil); err != nil {
			return err
		}
		if err := s.do("edit template", "PATCH", one, base+"/"+s.template, map[string]any{"description": "bucketmap"}, nil); err != nil {
			return err
		}
		return s.do("sync template", "PUT", one, base+"/"+s.template, nil, nil)
	})
}

// exerciseCommunity covers what only a community guild has: announcement
// channels, stage channels, the welcome screen and membership screening.
func (s *scenario) exerciseCommunity() {
	g := s.g()
	var welcome map[string]any
	// A guild that never set a welcome screen or a screening form answers 404
	// (10069, 10068): the edit then sets a disabled one, which changes
	// nothing members see.
	s.communityOnly("welcome screen", func() error {
		body := map[string]any{"enabled": false}
		if err := s.do("read welcome screen", "GET", "/guilds/{guild_id}/welcome-screen", g+"/welcome-screen", nil, &welcome); err == nil {
			body = map[string]any{"description": welcome["description"]}
		} else if !isStatus(err, 404) {
			return err
		}
		return s.do("edit welcome screen", "PATCH", "/guilds/{guild_id}/welcome-screen", g+"/welcome-screen", body, nil)
	})
	s.communityOnly("read new member welcome", func() error {
		return s.do("read new member welcome", "GET", "/guilds/{guild_id}/new-member-welcome", g+"/new-member-welcome", nil, nil)
	})
	s.communityOnly("membership screening", func() error {
		var form map[string]any
		body := map[string]any{"enabled": false}
		if err := s.do("read membership screening", "GET", "/guilds/{guild_id}/member-verification", g+"/member-verification", nil, &form); err == nil {
			body = map[string]any{"description": form["description"]}
		} else if !isStatus(err, 404) {
			return err
		}
		return s.do("edit membership screening", "PATCH", "/guilds/{guild_id}/member-verification", g+"/member-verification", body, nil)
	})
	s.communityOnly("rewrite onboarding as it is", func() error {
		var onboarding map[string]any
		if err := s.do("read onboarding before rewriting", "GET", "/guilds/{guild_id}/onboarding", g+"/onboarding", nil, &onboarding); err != nil {
			return err
		}
		body := map[string]any{}
		for _, k := range []string{"prompts", "default_channel_ids", "enabled", "mode"} {
			if v, ok := onboarding[k]; ok {
				body[k] = v
			}
		}
		return s.do("rewrite onboarding", "PUT", "/guilds/{guild_id}/onboarding", g+"/onboarding", body, nil)
	})

	var news, stage string
	s.communityOnly("announcement channel", func() error {
		var ch object
		if err := s.do("create announcement channel", "POST", "/guilds/{guild_id}/channels", g+"/channels", map[string]any{"name": s.cfg.Marker + "-news", "type": 5, "parent_id": nilIfEmpty(s.category)}, &ch); err != nil {
			return err
		}
		news = ch.ID
		s.extra = append(s.extra, named{"announcement channel", news})
		var m object
		if err := s.do("send announcement", "POST", "/channels/{channel_id}/messages", "/channels/"+news+"/messages", map[string]any{"content": "bucketmap announcement"}, &m); err != nil {
			return err
		}
		if err := s.do("publish announcement", "POST", "/channels/{channel_id}/messages/{message_id}/crosspost", "/channels/"+news+"/messages/"+m.ID+"/crosspost", nil, nil); err != nil {
			return err
		}
		if err := s.need(s.text2); err != nil {
			return err
		}
		var follow struct {
			WebhookID string `json:"webhook_id"`
		}
		if err := s.do("follow announcement channel", "POST", "/channels/{channel_id}/followers", "/channels/"+news+"/followers", map[string]any{"webhook_channel_id": s.text2}, &follow); err != nil {
			return err
		}
		if follow.WebhookID == "" {
			return nil
		}
		return s.do("unfollow announcement channel", "DELETE", "/webhooks/{webhook_id}", "/webhooks/"+follow.WebhookID, nil, nil)
	})
	s.communityOnly("stage instance", func() error {
		var ch object
		if err := s.do("create stage channel", "POST", "/guilds/{guild_id}/channels", g+"/channels", map[string]any{"name": s.cfg.Marker + "-stage", "type": 13, "parent_id": nilIfEmpty(s.category)}, &ch); err != nil {
			return err
		}
		stage = ch.ID
		s.extra = append(s.extra, named{"stage channel", stage})
		route := "/stage-instances/{channel_id}"
		if err := s.do("start stage instance", "POST", "/stage-instances", "/stage-instances", map[string]any{"channel_id": stage, "topic": s.cfg.Marker}, nil); err != nil {
			return err
		}
		if err := s.do("read stage instance", "GET", route, "/stage-instances/"+stage, nil, nil); err != nil {
			return err
		}
		if err := s.do("edit stage instance", "PATCH", route, "/stage-instances/"+stage, map[string]any{"topic": s.cfg.Marker + "-edited"}, nil); err != nil {
			return err
		}
		return s.do("end stage instance", "DELETE", route, "/stage-instances/"+stage, nil, nil)
	})
	s.optional("edit voice states on the stage", func() error {
		if stage == "" {
			return skip("no stage channel")
		}
		if err := s.do("edit own voice state", "PATCH", "/guilds/{guild_id}/voice-states/@me", g+"/voice-states/@me", map[string]any{"channel_id": stage, "suppress": true}, nil); err != nil {
			return err
		}
		if len(s.cfg.Users) == 0 {
			return afterFailure
		}
		return s.do("edit a member's voice state", "PATCH", "/guilds/{guild_id}/voice-states/{user_id}", g+"/voice-states/"+s.cfg.Users[0], map[string]any{"channel_id": stage, "suppress": true}, nil)
	})
}

// moderate comes last, in the order that keeps the test users usable as long
// as possible: a timeout is lifted at once, and kicking or banning removes a
// user from the guild, who must then rejoin through the invite printed at the
// end. Each needs an explicit flag.
func (s *scenario) moderate() {
	g := s.g()
	users := s.cfg.Users
	s.step("timeout test user 1", func() error {
		if len(users) < 1 {
			return afterFailure
		}
		route := "/guilds/{guild_id}/members/{user_id}"
		until := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		if err := s.do("timeout test user 1", "PATCH", route, g+"/members/"+users[0], map[string]any{"communication_disabled_until": until}, nil); err != nil {
			return err
		}
		return s.do("lift timeout of test user 1", "PATCH", route, g+"/members/"+users[0], map[string]any{"communication_disabled_until": nil}, nil)
	})
	if s.cfg.AllowPrune {
		s.step("prune inactive members", func() error {
			return s.do("prune inactive members", "POST", "/guilds/{guild_id}/prune", g+"/prune", map[string]any{"days": 30, "compute_prune_count": false}, nil)
		})
	}
	if s.cfg.AllowKick && len(users) >= 3 {
		s.step("kick test user 3", func() error {
			return s.do("kick test user 3", "DELETE", "/guilds/{guild_id}/members/{user_id}", g+"/members/"+users[2], nil, nil)
		})
	}
	if s.cfg.AllowBan && len(users) >= 4 {
		route := "/guilds/{guild_id}/bans/{user_id}"
		path := g + "/bans/" + users[3]
		s.step("ban test user 4", func() error {
			return s.do("ban test user 4", "PUT", route, path, map[string]any{"delete_message_seconds": 0}, nil)
		})
		s.step("read ban of test user 4", func() error { return s.do("read ban of test user 4", "GET", route, path, nil, nil) })
		s.step("unban test user 4", func() error { return s.do("unban test user 4", "DELETE", route, path, nil, nil) })
		s.step("bulk ban test user 4", func() error {
			if err := s.do("bulk ban test user 4", "POST", "/guilds/{guild_id}/bulk-ban", g+"/bulk-ban", map[string]any{"user_ids": []string{users[3]}}, nil); err != nil {
				return err
			}
			return s.do("unban test user 4 after the bulk ban", "DELETE", route, path, nil, nil)
		})
		s.optional("bulk ban test user 4, newer route", func() error {
			if err := s.do("bulk ban test user 4, newer route", "POST", "/guilds/{guild_id}/bulk-ban/v2", g+"/bulk-ban/v2", map[string]any{"user_ids": []string{users[3]}}, nil); err != nil {
				return err
			}
			return s.do("unban test user 4 after the newer bulk ban", "DELETE", route, path, nil, nil)
		})
	}
}
