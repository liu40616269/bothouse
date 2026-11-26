package discord

import (
	"context"
	"encoding/base64"
	"fmt"

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
