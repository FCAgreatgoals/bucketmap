package engine

// exerciseChannel edits the text channel: slowmode twice in a row, to observe
// the channel bucket's model, then a single rename. One rename only: renames
// and topic changes sit behind a sub-limit Discord does not announce in its
// headers, so a second one could only be seen through a 429.
func (s *scenario) exerciseChannel() {
	ch := s.text
	s.step("read channel", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("read channel", "GET", "/channels/{channel_id}", "/channels/"+ch, nil, nil)
	})
	s.step("toggle slowmode", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		if err := s.do("enable slowmode", "PATCH", "/channels/{channel_id}", "/channels/"+ch, map[string]any{"rate_limit_per_user": 10}, nil); err != nil {
			return err
		}
		return s.c.do(call{step: "disable slowmode", method: "PATCH", route: "/channels/{channel_id}", path: "/channels/" + ch, body: map[string]any{"rate_limit_per_user": 0}, immediate: true}, nil)
	})
	s.step("rename channel", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("rename channel", "PATCH", "/channels/{channel_id}", "/channels/"+ch, map[string]any{"name": s.cfg.Marker + "-renamed"}, nil)
	})
	s.step("reorder channels", func() error {
		if err := s.need(s.text, s.text2); err != nil {
			return err
		}
		swap := []map[string]any{{"id": s.text, "position": 1}, {"id": s.text2, "position": 0}}
		back := []map[string]any{{"id": s.text, "position": 0}, {"id": s.text2, "position": 1}}
		if err := s.do("reorder channels", "PATCH", "/guilds/{guild_id}/channels", s.g()+"/channels", swap, nil); err != nil {
			return err
		}
		return s.do("restore channel order", "PATCH", "/guilds/{guild_id}/channels", s.g()+"/channels", back, nil)
	})
	s.optional("set voice channel status", func() error {
		if err := s.need(s.voice); err != nil {
			return err
		}
		return s.do("set voice channel status", "PUT", "/channels/{channel_id}/voice-status", "/channels/"+s.voice+"/voice-status", map[string]any{"status": s.cfg.Marker}, nil)
	})
}

func (s *scenario) exerciseInvites() {
	ch := s.text
	var invite object
	s.step("create invite", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("create invite", "POST", "/channels/{channel_id}/invites", "/channels/"+ch+"/invites", map[string]any{"max_age": 3600, "unique": true}, &invite)
	})
	s.step("list channel invites", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("list channel invites", "GET", "/channels/{channel_id}/invites", "/channels/"+ch+"/invites", nil, nil)
	})
	s.step("list guild invites", func() error {
		return s.do("list guild invites", "GET", "/guilds/{guild_id}/invites", s.g()+"/invites", nil, nil)
	})
	s.step("read invite", func() error {
		if err := s.need(invite.Code); err != nil {
			return err
		}
		return s.do("read invite", "GET", "/invites/{code}", "/invites/"+invite.Code+"?with_counts=true", nil, nil)
	})
	// Invites restricted to chosen users are recent and not open to every
	// application.
	s.optional("restrict invite to test user 1", func() error {
		if err := s.need(invite.Code); err != nil {
			return err
		}
		if len(s.cfg.Users) == 0 {
			return afterFailure
		}
		base := "/invites/" + invite.Code + "/target-users"
		route := "/invites/{code}/target-users"
		user := s.cfg.Users[0]
		if err := s.do("add invite target user", "PUT", route+"/{user_id}", base+"/"+user, nil, nil); err != nil {
			return err
		}
		if err := s.do("list invite target users", "GET", route, base, nil, nil); err != nil {
			return err
		}
		if len(s.cfg.Users) > 1 {
			other := s.cfg.Users[1]
			if err := s.do("add invite target users in bulk", "POST", route+"/bulk-add", base+"/bulk-add", map[string]any{"user_ids": []string{other}}, nil); err != nil {
				return err
			}
			// No job left to report answers 404 (10124): the route still ran.
			if err := s.do("read invite targeting job", "GET", route+"/job-status", base+"/job-status", nil, nil); err != nil && !isStatus(err, 404) {
				return err
			}
			if err := s.do("remove invite target users in bulk", "POST", route+"/bulk-delete", base+"/bulk-delete", map[string]any{"user_ids": []string{other}}, nil); err != nil {
				return err
			}
		}
		// The whole list at once, as the CSV file Discord takes.
		csv := multipartBody{files: []file{{field: "target_users_file", name: "targets.csv", contentType: "text/csv", data: []byte("user_id\n" + user + "\n")}}}
		if err := s.do("replace invite target users from a file", "PUT", route, base, csv, nil); err != nil {
			return err
		}
		return s.do("remove invite target user", "DELETE", route+"/{user_id}", base+"/"+user, nil, nil)
	})
	s.step("delete invite", func() error {
		if err := s.need(invite.Code); err != nil {
			return err
		}
		return s.do("delete invite", "DELETE", "/invites/{code}", "/invites/"+invite.Code, nil, nil)
	})
}

// exerciseWebhooks covers the routes a bot reaches with its token and those
// reached with the webhook's own token. Discord counts each webhook token
// apart, which is what the token routes check.
func (s *scenario) exerciseWebhooks() {
	ch := s.text
	s.step("create webhook", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("create webhook", "POST", "/channels/{channel_id}/webhooks", "/channels/"+ch+"/webhooks", map[string]any{"name": s.cfg.Marker}, &s.webhook)
	})
	s.step("read webhook", func() error {
		if err := s.need(s.webhook.ID); err != nil {
			return err
		}
		return s.do("read webhook", "GET", "/webhooks/{webhook_id}", "/webhooks/"+s.webhook.ID, nil, nil)
	})
	s.step("edit webhook", func() error {
		if err := s.need(s.webhook.ID); err != nil {
			return err
		}
		return s.do("edit webhook", "PATCH", "/webhooks/{webhook_id}", "/webhooks/"+s.webhook.ID, map[string]any{"name": s.cfg.Marker + "-edited"}, nil)
	})
	s.step("list channel webhooks", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("list channel webhooks", "GET", "/channels/{channel_id}/webhooks", "/channels/"+ch+"/webhooks", nil, nil)
	})
	s.step("list guild webhooks", func() error {
		return s.do("list guild webhooks", "GET", "/guilds/{guild_id}/webhooks", s.g()+"/webhooks", nil, nil)
	})

	token := "/webhooks/{webhook_id}/{webhook_token}"
	base := "/webhooks/" + s.webhook.ID + "/" + s.webhook.Token
	s.step("read webhook with its token", func() error {
		if err := s.need(s.webhook.ID, s.webhook.Token); err != nil {
			return err
		}
		return s.do("read webhook with its token", "GET", token, base, nil, nil)
	})
	s.step("edit webhook with its token", func() error {
		if err := s.need(s.webhook.ID, s.webhook.Token); err != nil {
			return err
		}
		return s.do("edit webhook with its token", "PATCH", token, base, map[string]any{"name": s.cfg.Marker}, nil)
	})

	var sent object
	s.step("execute webhook", func() error {
		if err := s.need(s.webhook.ID, s.webhook.Token); err != nil {
			return err
		}
		return s.do("execute webhook", "POST", token, base+"?wait=true", map[string]any{"content": "bucketmap webhook message"}, &sent)
	})
	msgRoute := token + "/messages/{message_id}"
	for _, m := range []struct {
		step, method string
		body         any
	}{
		{"read webhook message", "GET", nil},
		{"edit webhook message", "PATCH", map[string]any{"content": "bucketmap webhook message, edited"}},
		{"delete webhook message", "DELETE", nil},
	} {
		s.step(m.step, func() error {
			if err := s.need(sent.ID); err != nil {
				return err
			}
			return s.do(m.step, m.method, msgRoute, base+"/messages/"+sent.ID, m.body, nil)
		})
	}

	// A second webhook, deleted with its own token rather than the bot's.
	s.step("create a second webhook", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("create a second webhook", "POST", "/channels/{channel_id}/webhooks", "/channels/"+ch+"/webhooks", map[string]any{"name": s.cfg.Marker + "-2"}, &s.webhook2)
	})
	s.step("delete webhook with its token", func() error {
		if err := s.need(s.webhook2.ID, s.webhook2.Token); err != nil {
			return err
		}
		err := s.do("delete webhook with its token", "DELETE", token, "/webhooks/"+s.webhook2.ID+"/"+s.webhook2.Token, nil, nil)
		if err == nil {
			s.webhook2 = object{}
		}
		return err
	})
}
