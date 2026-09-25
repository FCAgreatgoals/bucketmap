package engine

import (
	"fmt"
	"net/url"
)

// keycap is the #️⃣ emoji, percent-encoded as a client sends it. A proxy that
// rebuilds the URL from the decoded path turns its # into a fragment and
// truncates the request, which is why it is exercised.
const keycap = "%23%EF%B8%8F%E2%83%A3"

var thumbsUp = url.PathEscape("👍")

func (s *scenario) msg(i int) string {
	if i < len(s.messages) {
		return s.messages[i]
	}
	return ""
}

func (s *scenario) exerciseMessages() {
	ch := s.text
	for i := 1; i <= 8; i++ {
		name := fmt.Sprintf("send message %d", i)
		s.step(name, func() error {
			if err := s.need(ch); err != nil {
				return err
			}
			var m object
			err := s.do(name, "POST", "/channels/{channel_id}/messages", "/channels/"+ch+"/messages", map[string]any{"content": fmt.Sprintf("bucketmap message %d", i)}, &m)
			if err == nil {
				s.messages = append(s.messages, m.ID)
			}
			return err
		})
	}

	s.step("list messages", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.pair("list messages", "GET", "/channels/{channel_id}/messages", "/channels/"+ch+"/messages?limit=10", nil)
	})
	s.step("read message", func() error {
		if err := s.need(ch, s.msg(0)); err != nil {
			return err
		}
		return s.do("read message", "GET", "/channels/{channel_id}/messages/{message_id}", "/channels/"+ch+"/messages/"+s.msg(0), nil, nil)
	})
	s.step("edit message", func() error {
		if err := s.need(ch, s.msg(0)); err != nil {
			return err
		}
		return s.do("edit message", "PATCH", "/channels/{channel_id}/messages/{message_id}", "/channels/"+ch+"/messages/"+s.msg(0), map[string]any{"content": "bucketmap message 1, edited"}, nil)
	})
	s.step("show typing", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("show typing", "POST", "/channels/{channel_id}/typing", "/channels/"+ch+"/typing", nil, nil)
	})

	s.step("delete message", func() error {
		if err := s.need(ch, s.msg(5)); err != nil {
			return err
		}
		return s.do("delete message", "DELETE", "/channels/{channel_id}/messages/{message_id}", "/channels/"+ch+"/messages/"+s.msg(5), nil, nil)
	})
	s.step("bulk delete messages", func() error {
		if err := s.need(ch, s.msg(3), s.msg(4)); err != nil {
			return err
		}
		return s.do("bulk delete messages", "POST", "/channels/{channel_id}/messages/bulk-delete", "/channels/"+ch+"/messages/bulk-delete", map[string]any{"messages": []string{s.msg(3), s.msg(4)}}, nil)
	})
}

// exerciseReactions covers every reaction route, on one message: adding, the
// keycap emoji a proxy may truncate, listing, and the four ways of removing.
func (s *scenario) exerciseReactions() {
	ch, m := s.text, s.msg(0)
	base := "/channels/" + ch + "/messages/" + m + "/reactions"
	own := "/channels/{channel_id}/messages/{message_id}/reactions/{emoji_name}/@me"
	react := func(step, emoji string) {
		s.step(step, func() error {
			if err := s.need(ch, m); err != nil {
				return err
			}
			return s.do(step, "PUT", own, base+"/"+emoji+"/@me", nil, nil)
		})
	}

	react("react", thumbsUp)
	react("react with a keycap emoji", keycap)
	s.step("list reactions", func() error {
		if err := s.need(ch, m); err != nil {
			return err
		}
		return s.do("list reactions", "GET", "/channels/{channel_id}/messages/{message_id}/reactions/{emoji_name}", base+"/"+thumbsUp, nil, nil)
	})
	s.step("remove own reaction", func() error {
		if err := s.need(ch, m); err != nil {
			return err
		}
		return s.do("remove own reaction", "DELETE", own, base+"/"+keycap+"/@me", nil, nil)
	})
	s.step("remove a user's reaction", func() error {
		if err := s.need(ch, m, s.botID); err != nil {
			return err
		}
		return s.do("remove a user's reaction", "DELETE", "/channels/{channel_id}/messages/{message_id}/reactions/{emoji_name}/{user_id}", base+"/"+thumbsUp+"/"+s.botID, nil, nil)
	})
	react("react again", thumbsUp)
	s.step("remove all reactions for an emoji", func() error {
		if err := s.need(ch, m); err != nil {
			return err
		}
		return s.do("remove all reactions for an emoji", "DELETE", "/channels/{channel_id}/messages/{message_id}/reactions/{emoji_name}", base+"/"+thumbsUp, nil, nil)
	})
	react("react once more", thumbsUp)
	s.step("remove all reactions", func() error {
		if err := s.need(ch, m); err != nil {
			return err
		}
		return s.do("remove all reactions", "DELETE", "/channels/{channel_id}/messages/{message_id}/reactions", base, nil, nil)
	})

	// Super reactions carry their type in the path. Documented by the
	// community, not by Discord.
	s.optional("react, then remove with the reaction type", func() error {
		if err := s.need(ch, m); err != nil {
			return err
		}
		if err := s.do("react before removing with the type", "PUT", own, base+"/"+thumbsUp+"/@me", nil, nil); err != nil {
			return err
		}
		return s.do("remove own reaction with the type", "DELETE", "/channels/{channel_id}/messages/{message_id}/reactions/{emoji}/{reaction_type}/@me", base+"/"+thumbsUp+"/0/@me", nil, nil)
	})
	s.optional("react, then remove a user's reaction with the type", func() error {
		if err := s.need(ch, m, s.botID); err != nil {
			return err
		}
		if err := s.do("react before removing a user's with the type", "PUT", own, base+"/"+thumbsUp+"/@me", nil, nil); err != nil {
			return err
		}
		return s.do("remove a user's reaction with the type", "DELETE", "/channels/{channel_id}/messages/{message_id}/reactions/{emoji}/{reaction_type}/{user_id}", base+"/"+thumbsUp+"/0/"+s.botID, nil, nil)
	})
}

// exercisePins covers both families of pin routes: the current ones under
// /messages/pins and the older ones Discord still answers.
func (s *scenario) exercisePins() {
	ch := s.text
	for _, v := range []struct{ label, base, route string }{
		{"", "/channels/" + ch + "/messages/pins", "/channels/{channel_id}/messages/pins"},
		{" (older route)", "/channels/" + ch + "/pins", "/channels/{channel_id}/pins"},
	} {
		s.step("pin message"+v.label, func() error {
			if err := s.need(ch, s.msg(1)); err != nil {
				return err
			}
			return s.do("pin message"+v.label, "PUT", v.route+"/{message_id}", v.base+"/"+s.msg(1), nil, nil)
		})
		s.step("list pins"+v.label, func() error {
			if err := s.need(ch); err != nil {
				return err
			}
			return s.do("list pins"+v.label, "GET", v.route, v.base, nil, nil)
		})
		s.step("unpin message"+v.label, func() error {
			if err := s.need(ch, s.msg(1)); err != nil {
				return err
			}
			return s.do("unpin message"+v.label, "DELETE", v.route+"/{message_id}", v.base+"/"+s.msg(1), nil, nil)
		})
	}
}

func (s *scenario) exerciseThreads() {
	ch := s.text
	s.step("start thread from message", func() error {
		if err := s.need(ch, s.msg(2)); err != nil {
			return err
		}
		var t object
		err := s.do("start thread from message", "POST", "/channels/{channel_id}/messages/{message_id}/threads", "/channels/"+ch+"/messages/"+s.msg(2)+"/threads", map[string]any{"name": s.cfg.Marker + "-thread"}, &t)
		if err == nil {
			s.extra = append(s.extra, named{"thread", t.ID})
		}
		return err
	})
	var private string
	s.step("start thread", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		var t object
		err := s.do("start thread", "POST", "/channels/{channel_id}/threads", "/channels/"+ch+"/threads", map[string]any{"name": s.cfg.Marker + "-thread-2", "type": 12}, &t)
		if err == nil {
			s.extra = append(s.extra, named{"private thread", t.ID})
			private = t.ID
		}
		return err
	})

	members := "/channels/{channel_id}/thread-members"
	s.step("join and leave thread", func() error {
		if err := s.need(private); err != nil {
			return err
		}
		if err := s.do("join thread", "PUT", members+"/@me", "/channels/"+private+"/thread-members/@me", nil, nil); err != nil {
			return err
		}
		return s.do("leave thread", "DELETE", members+"/@me", "/channels/"+private+"/thread-members/@me", nil, nil)
	})
	s.step("manage thread members", func() error {
		if err := s.need(private); err != nil {
			return err
		}
		if len(s.cfg.Users) == 0 {
			return afterFailure
		}
		user := s.cfg.Users[0]
		base := "/channels/" + private + "/thread-members"
		if err := s.do("add test user 1 to thread", "PUT", members+"/{user_id}", base+"/"+user, nil, nil); err != nil {
			return err
		}
		if err := s.do("list thread members", "GET", members, base+"?with_member=true", nil, nil); err != nil {
			return err
		}
		if err := s.do("read thread member", "GET", members+"/{user_id}", base+"/"+user, nil, nil); err != nil {
			return err
		}
		return s.do("remove test user 1 from thread", "DELETE", members+"/{user_id}", base+"/"+user, nil, nil)
	})

	s.step("list active threads", func() error {
		return s.do("list active threads", "GET", "/guilds/{guild_id}/threads/active", s.g()+"/threads/active", nil, nil)
	})
	s.optional("list active threads of a channel", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("list active threads of a channel", "GET", "/channels/{channel_id}/threads/active", "/channels/"+ch+"/threads/active", nil, nil)
	})
	for _, r := range []struct{ step, route, path string }{
		{"list public archived threads", "/channels/{channel_id}/threads/archived/public", "/channels/" + ch + "/threads/archived/public"},
		{"list private archived threads", "/channels/{channel_id}/threads/archived/private", "/channels/" + ch + "/threads/archived/private"},
		{"list joined private archived threads", "/channels/{channel_id}/users/@me/threads/archived/private", "/channels/" + ch + "/users/@me/threads/archived/private"},
	} {
		s.step(r.step, func() error {
			if err := s.need(ch); err != nil {
				return err
			}
			return s.do(r.step, "GET", r.route, r.path, nil, nil)
		})
	}

	// Forum routes, on a forum channel when the guild allows one.
	s.optional("search forum threads", func() error {
		if s.forum == "" {
			return skip("no forum channel")
		}
		return s.do("search forum threads", "GET", "/channels/{channel_id}/threads/search", "/channels/"+s.forum+"/threads/search?limit=5", nil, nil)
	})
	s.optional("manage forum tags", func() error {
		if s.forum == "" {
			return skip("no forum channel")
		}
		var ch struct {
			AvailableTags []object `json:"available_tags"`
		}
		base := "/channels/" + s.forum + "/tags"
		body := map[string]any{"name": s.cfg.Marker}
		if err := s.do("create forum tag", "POST", "/channels/{channel_id}/tags", base, body, &ch); err != nil {
			return err
		}
		var withTwin struct {
			AvailableTags []object `json:"available_tags"`
		}
		if err := s.twin("create forum tag", "/channels/{channel_id}/tags", base, body, "-2", &withTwin); err != nil {
			return err
		}
		if n := len(withTwin.AvailableTags); n > 0 {
			defer s.dropTwin("delete forum tag", "/channels/{channel_id}/tags/{forum_tag_id}", base+"/"+withTwin.AvailableTags[n-1].ID)
		}
		if len(ch.AvailableTags) == 0 {
			return afterFailure
		}
		tag := ch.AvailableTags[len(ch.AvailableTags)-1].ID
		if err := s.do("edit forum tag", "PUT", "/channels/{channel_id}/tags/{forum_tag_id}", base+"/"+tag, map[string]any{"name": s.cfg.Marker + "-2"}, nil); err != nil {
			return err
		}
		return s.do("delete forum tag", "DELETE", "/channels/{channel_id}/tags/{forum_tag_id}", base+"/"+tag, nil, nil)
	})
}

// exerciseAttachments sends a message with a file, which a proxy must forward
// as multipart without touching it, then covers the cloud upload routes.
func (s *scenario) exerciseAttachments() {
	ch := s.text
	var sent struct {
		Attachments []struct {
			URL string `json:"url"`
		} `json:"attachments"`
	}
	s.step("send message with a file", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		body := multipartBody{
			payload: map[string]any{"content": "bucketmap attachment", "attachments": []map[string]any{{"id": 0, "filename": "bucketmap.txt"}}},
			files:   []file{{name: "bucketmap.txt", contentType: "text/plain", data: []byte("bucketmap\n")}},
		}
		return s.do("send message with a file", "POST", "/channels/{channel_id}/messages", "/channels/"+ch+"/messages", body, &sent)
	})
	s.optional("refresh attachment URLs", func() error {
		if len(sent.Attachments) == 0 {
			return afterFailure
		}
		return s.do("refresh attachment URLs", "POST", "/attachments/refresh-urls", "/attachments/refresh-urls", map[string]any{"attachment_urls": []string{sent.Attachments[0].URL}}, nil)
	})
	s.optional("request an upload URL, then drop it", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		var up struct {
			Attachments []struct {
				UploadFilename string `json:"upload_filename"`
			} `json:"attachments"`
		}
		if err := s.do("request an upload URL", "POST", "/channels/{channel_id}/attachments", "/channels/"+ch+"/attachments", map[string]any{"files": []map[string]any{{"id": "0", "filename": "bucketmap.txt", "file_size": 10}}}, &up); err != nil {
			return err
		}
		if len(up.Attachments) == 0 || up.Attachments[0].UploadFilename == "" {
			return afterFailure
		}
		return s.do("drop the upload", "DELETE", "/attachments/{upload_filename}", "/attachments/"+up.Attachments[0].UploadFilename, nil, nil)
	})
}

func (s *scenario) exercisePolls() {
	ch := s.text
	var poll object
	s.step("send a poll", func() error {
		if err := s.need(ch); err != nil {
			return err
		}
		return s.do("send a poll", "POST", "/channels/{channel_id}/messages", "/channels/"+ch+"/messages", map[string]any{"poll": map[string]any{
			"question": map[string]any{"text": "bucketmap poll"},
			"answers":  []map[string]any{{"poll_media": map[string]any{"text": "yes"}}, {"poll_media": map[string]any{"text": "no"}}},
			"duration": 1,
		}}, &poll)
	})
	s.step("list poll voters", func() error {
		if err := s.need(ch, poll.ID); err != nil {
			return err
		}
		return s.do("list poll voters", "GET", "/channels/{channel_id}/polls/{message_id}/answers/{answer_id}", "/channels/"+ch+"/polls/"+poll.ID+"/answers/1", nil, nil)
	})
	s.step("end the poll", func() error {
		if err := s.need(ch, poll.ID); err != nil {
			return err
		}
		return s.do("end the poll", "POST", "/channels/{channel_id}/polls/{message_id}/expire", "/channels/"+ch+"/polls/"+poll.ID+"/expire", nil, nil)
	})
}
