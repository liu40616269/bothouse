package discord

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// BotAvatarURL implements core.BotAvatarGetter.
func (p *Platform) BotAvatarURL() string {
	p.mu.RLock()
	s := p.session
	p.mu.RUnlock()
	if s == nil || s.State == nil || s.State.User == nil {
		return ""
	}
	return s.State.User.AvatarURL("256")
}

// BotUsername implements core.BotNameUpdater.
func (p *Platform) BotUsername() string {
	p.mu.RLock()
	s := p.session
	p.mu.RUnlock()
	if s != nil && s.State != nil && s.State.User != nil && s.State.User.Username != "" {
		return s.State.User.Username
	}
	return p.botName
}

// UpdateBotAvatar implements core.BotAvatarUpdater by calling Discord's
// PATCH /users/@me endpoint with the bot's configured token.
func (p *Platform) UpdateBotAvatar(ctx context.Context, mimeType string, imgBytes []byte) (string, error) {
	p.mu.RLock()
	s := p.session
	p.mu.RUnlock()
	if s == nil {
		return "", fmt.Errorf("discord session is not connected")
	}
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(imgBytes))
	user, err := s.UserUpdate("", dataURI, "", discordgo.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("discord update avatar failed: %w", err)
	}
	if user == nil {
		return "", nil
	}
	if s.State != nil {
		s.State.Lock()
		s.State.User = user
		s.State.Unlock()
	}
	return user.AvatarURL("256"), nil
}

// UpdateBotName implements core.BotNameUpdater. It updates the bot's server
// nickname across all joined guilds (instant, minimal rate limit) and also
// attempts to update the global bot username when >= 2 chars; if global update
// hits Discord's rate limit after guild nicknames succeed, it still succeeds.
func (p *Platform) UpdateBotName(ctx context.Context, name string) (int, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false, fmt.Errorf("name cannot be empty")
	}
	if len([]rune(name)) > 32 {
		return 0, false, fmt.Errorf("name exceeds Discord's 32-character limit")
	}

	p.mu.RLock()
	s := p.session
	p.mu.RUnlock()
	if s == nil {
		return 0, false, fmt.Errorf("discord session is not connected")
	}

	guildIDs := map[string]bool{}
	if p.guildID != "" {
		guildIDs[p.guildID] = true
	}
	if s.State != nil {
		s.State.RLock()
		for _, g := range s.State.Guilds {
			if g != nil && g.ID != "" {
				guildIDs[g.ID] = true
			}
		}
		s.State.RUnlock()
	}

	updatedGuilds := 0
	var lastGuildErr error
	for gid := range guildIDs {
		if err := s.GuildMemberNickname(gid, "@me", name, discordgo.WithContext(ctx)); err != nil {
			lastGuildErr = err
		} else {
			updatedGuilds++
		}
	}

	globalUpdated := false
	var globalErr error
	if len([]rune(name)) >= 2 {
		currentUsername := ""
		if s.State != nil && s.State.User != nil {
			currentUsername = s.State.User.Username
		}
		if currentUsername == name {
			globalUpdated = true
		} else {
			user, err := s.UserUpdate(name, "", "", discordgo.WithContext(ctx))
			if err != nil {
				globalErr = err
			} else if user != nil {
				globalUpdated = true
				p.botName = user.Username
				if s.State != nil {
					s.State.Lock()
					s.State.User = user
					s.State.Unlock()
				}
			}
		}
	}

	if updatedGuilds == 0 && !globalUpdated {
		if globalErr != nil {
			return 0, false, fmt.Errorf("discord update name failed: %w", globalErr)
		}
		if lastGuildErr != nil {
			return 0, false, fmt.Errorf("discord update nickname failed: %w", lastGuildErr)
		}
		return 0, false, fmt.Errorf("discord bot is not in any server and global name update was skipped")
	}
	return updatedGuilds, globalUpdated, nil
}
