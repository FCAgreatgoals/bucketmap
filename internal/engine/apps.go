package engine

import (
	"encoding/base64"
	"errors"
)

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func (s *scenario) app() string { return "/applications/" + s.appID }

func (s *scenario) exerciseApplication() {
	s.step("read application", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		return s.do("read application", "GET", "/applications/{application_id}", s.app(), nil, nil)
	})

	// An empty edit changes nothing, and still goes through the route.
	s.optional("edit application with nothing", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		if err := s.do("edit current application with nothing", "PATCH", "/applications/@me", "/applications/@me", map[string]any{}, nil); err != nil {
			return err
		}
		return s.do("edit application with nothing", "PATCH", "/applications/{application_id}", s.app(), map[string]any{}, nil)
	})
	s.step("rewrite role connection metadata as it is", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		route := "/applications/{application_id}/role-connections/metadata"
		var metadata []map[string]any
		if err := s.do("read role connection metadata", "GET", route, s.app()+"/role-connections/metadata", nil, &metadata); err != nil {
			return err
		}
		if metadata == nil {
			return skip("the metadata could not be read")
		}
		return s.do("rewrite role connection metadata", "PUT", route, s.app()+"/role-connections/metadata", metadata, nil)
	})

	// Application emojis belong to the application, not to a guild, and only
	// the application can use them.
	list := "/applications/{application_id}/emojis"
	one := list + "/{emoji_id}"
	s.step("list application emojis", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		return s.do("list application emojis", "GET", list, s.app()+"/emojis", nil, nil)
	})
	s.optional("create application emoji", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		var e object
		body := map[string]any{"name": "bucketmap", "image": tinyPNG}
		if err := s.do("create application emoji", "POST", list, s.app()+"/emojis", body, &e); err != nil {
			return err
		}
		s.appEmoji = e.ID
		var twin object
		err := s.twin("create application emoji", list, s.app()+"/emojis", body, "_twin", &twin)
		s.appEmoji2 = twin.ID
		return err
	})
	s.step("use application emoji", func() error {
		if s.appEmoji == "" {
			return skip("no application emoji was created")
		}
		if err := s.do("read application emoji", "GET", one, s.app()+"/emojis/"+s.appEmoji, nil, nil); err != nil {
			return err
		}
		return s.do("rename application emoji", "PATCH", one, s.app()+"/emojis/"+s.appEmoji, map[string]any{"name": "bucketmap_2"}, nil)
	})
}

// exerciseCommands uses guild commands for everything that writes: global
// ones reach every guild the bot is in. Global commands are only read, unless
// explicitly allowed.
func (s *scenario) exerciseCommands() {
	base := s.app() + "/guilds/" + s.cfg.Guild + "/commands"
	list := "/applications/{application_id}/guilds/{guild_id}/commands"
	one := list + "/{command_id}"
	var cmd, cmd2 object
	var original []map[string]any
	s.step("list guild commands", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		return s.do("list guild commands", "GET", list, base, nil, &original)
	})
	s.step("create guild command", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		body := map[string]any{"name": "bucketmap", "description": "bucketmap test command", "type": 1}
		if err := s.do("create guild command", "POST", list, base, body, &cmd); err != nil {
			return err
		}
		return s.twin("create guild command", list, base, body, "-twin", &cmd2)
	})
	s.step("read guild command", func() error {
		if err := s.need(cmd.ID); err != nil {
			return err
		}
		return s.do("read guild command", "GET", one, base+"/"+cmd.ID, nil, nil)
	})
	s.step("edit guild command", func() error {
		if err := s.need(cmd.ID); err != nil {
			return err
		}
		return s.do("edit guild command", "PATCH", one, base+"/"+cmd.ID, map[string]any{"description": "bucketmap test command, edited"}, nil)
	})
	s.step("read command permissions", func() error {
		if err := s.need(cmd.ID); err != nil {
			return err
		}
		if err := s.do("list command permissions of the guild", "GET", list+"/permissions", base+"/permissions", nil, nil); err != nil {
			return err
		}
		// A command nobody set permissions on answers 404 Unknown application
		// command permissions: the route is still exercised.
		err := s.do("read command permissions", "GET", one+"/permissions", base+"/"+cmd.ID+"/permissions", nil, nil)
		var se *statusError
		if errors.As(err, &se) && se.status == 404 {
			return nil
		}
		return err
	})
	s.step("delete guild command", func() error {
		if err := s.need(cmd.ID); err != nil {
			return err
		}
		if err := s.do("delete guild command", "DELETE", one, base+"/"+cmd.ID, nil, nil); err != nil {
			return err
		}
		if cmd2.ID != "" {
			s.dropTwin("delete guild command", one, base+"/"+cmd2.ID)
		}
		return nil
	})
	// Rewriting the guild's commands as they were before the run exercises the
	// bulk overwrite without changing anything.
	s.step("overwrite guild commands with themselves", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		if original == nil {
			// Never overwrite with a list that was not read: an empty one
			// would delete every command of the guild.
			return skip("the guild's commands could not be read")
		}
		return s.do("overwrite guild commands with themselves", "PUT", list, base, original, nil)
	})

	global := "/applications/{application_id}/commands"
	var existing []object
	s.step("list global commands", func() error {
		if err := s.need(s.appID); err != nil {
			return err
		}
		return s.do("list global commands", "GET", global, s.app()+"/commands", nil, &existing)
	})
	s.step("read global command", func() error {
		if len(existing) == 0 {
			return skip("the application has no global command")
		}
		return s.do("read global command", "GET", global+"/{command_id}", s.app()+"/commands/"+existing[0].ID, nil, nil)
	})
	if s.cfg.AllowGlobalCommands {
		s.step("create, edit and delete a global command", func() error {
			var g, g2 object
			body := map[string]any{"name": "bucketmap", "description": "bucketmap test command", "type": 1}
			if err := s.do("create global command", "POST", global, s.app()+"/commands", body, &g); err != nil {
				return err
			}
			if err := s.twin("create global command", global, s.app()+"/commands", body, "-twin", &g2); err == nil && g2.ID != "" {
				defer s.dropTwin("delete global command", global+"/{command_id}", s.app()+"/commands/"+g2.ID)
			}
			if err := s.do("edit global command", "PATCH", global+"/{command_id}", s.app()+"/commands/"+g.ID, map[string]any{"description": "bucketmap test command, edited"}, nil); err != nil {
				return err
			}
			return s.do("delete global command", "DELETE", global+"/{command_id}", s.app()+"/commands/"+g.ID, nil, nil)
		})
	}
}

func (s *scenario) exerciseUsers() {
	s.step("list own guilds", func() error {
		return s.do("list own guilds", "GET", "/users/@me/guilds", "/users/@me/guilds?limit=10", nil, nil)
	})
	s.step("open a DM with test user 1", func() error {
		if len(s.cfg.Users) == 0 {
			return afterFailure
		}
		return s.do("open a DM with test user 1", "POST", "/users/@me/channels", "/users/@me/channels", map[string]any{"recipient_id": s.cfg.Users[0]}, nil)
	})
}

// exercisePublic covers routes tied to no guild.
func (s *scenario) exercisePublic() {
	s.step("read gateway URL", func() error { return s.do("read gateway URL", "GET", "/gateway", "/gateway", nil, nil) })
	s.step("list voice regions", func() error {
		return s.do("list voice regions", "GET", "/voice/regions", "/voice/regions", nil, nil)
	})
	var packs struct {
		StickerPacks []struct {
			ID string `json:"id"`
		} `json:"sticker_packs"`
	}
	s.step("list sticker packs", func() error {
		return s.do("list sticker packs", "GET", "/sticker-packs", "/sticker-packs", nil, &packs)
	})
	s.step("read sticker pack", func() error {
		if len(packs.StickerPacks) == 0 {
			return afterFailure
		}
		return s.do("read sticker pack", "GET", "/sticker-packs/{pack_id}", "/sticker-packs/"+packs.StickerPacks[0].ID, nil, nil)
	})
}
