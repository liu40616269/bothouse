package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// BotAvatarUpdater is an optional Platform interface for platforms that support
// updating the connected bot's profile avatar using its configured token.
type BotAvatarUpdater interface {
	UpdateBotAvatar(ctx context.Context, mimeType string, imgBytes []byte) (avatarURL string, err error)
}

// BotAvatarGetter is an optional Platform interface for reading the connected
// bot's current profile avatar URL.
type BotAvatarGetter interface {
	BotAvatarURL() string
}

// BotNameUpdater is an optional Platform interface for updating the connected
// bot's display name (server nickname and/or global username) using its token.
type BotNameUpdater interface {
	BotUsername() string
	UpdateBotName(ctx context.Context, name string) (updatedGuilds int, globalUpdated bool, err error)
}

const maxBotAvatarBytes = 8 * 1024 * 1024 // 8 MB (Discord avatar limit)

var allowedAvatarMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

func parseAvatarDataURL(dataURL string) (string, []byte, error) {
	s := strings.TrimSpace(dataURL)
	if !strings.HasPrefix(s, "data:") {
		return "", nil, fmt.Errorf("invalid image data URL")
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return "", nil, fmt.Errorf("malformed data URL")
	}
	meta := s[len("data:"):comma]
	encoded := s[comma+1:]
	if !strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return "", nil, fmt.Errorf("avatar data URL must be base64 encoded")
	}
	declaredMIME := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(meta, ";base64")))
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("invalid base64 image data: %w", err)
	}
	if len(raw) == 0 {
		return "", nil, fmt.Errorf("empty image file")
	}
	if len(raw) > maxBotAvatarBytes {
		return "", nil, fmt.Errorf("image exceeds 8MB limit")
	}
	detected := http.DetectContentType(raw)
	mimeType := detected
	if !allowedAvatarMIMEs[mimeType] && allowedAvatarMIMEs[declaredMIME] {
		mimeType = declaredMIME
	}
	if !allowedAvatarMIMEs[mimeType] {
		return "", nil, fmt.Errorf("unsupported image format %q (use PNG, JPEG, GIF, or WebP)", detected)
	}
	return mimeType, raw, nil
}

func (m *ManagementServer) handleProjectAvatar(w http.ResponseWriter, r *http.Request, e *Engine) {
	switch r.Method {
	case http.MethodGet:
		var platforms []string
		var avatarURL string
		var botUsername string
		var supportsName bool
		for _, p := range e.platforms {
			if _, ok := p.(BotAvatarUpdater); ok {
				platforms = append(platforms, p.Name())
			}
			if avatarURL == "" {
				if g, ok := p.(BotAvatarGetter); ok {
					avatarURL = g.BotAvatarURL()
				}
			}
			if nu, ok := p.(BotNameUpdater); ok {
				supportsName = true
				if botUsername == "" {
					botUsername = nu.BotUsername()
				}
			}
		}
		mgmtJSON(w, http.StatusOK, map[string]any{
			"supported":     len(platforms) > 0,
			"supports_name": supportsName,
			"platforms":     platforms,
			"avatar_url":    avatarURL,
			"bot_username":  botUsername,
		})

	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 12*1024*1024)
		var req struct {
			DataURL string `json:"data_url"`
			Name    string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mgmtError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		req.DataURL = strings.TrimSpace(req.DataURL)
		req.Name = strings.TrimSpace(req.Name)
		if req.DataURL == "" && req.Name == "" {
			mgmtError(w, http.StatusBadRequest, "either data_url or name is required")
			return
		}

		resp := map[string]any{}

		if req.DataURL != "" {
			mimeType, imgBytes, err := parseAvatarDataURL(req.DataURL)
			if err != nil {
				mgmtError(w, http.StatusBadRequest, err.Error())
				return
			}
			var updated []string
			var latestURL string
			var errs []string
			for _, p := range e.platforms {
				updater, ok := p.(BotAvatarUpdater)
				if !ok {
					continue
				}
				u, err := updater.UpdateBotAvatar(r.Context(), mimeType, imgBytes)
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
					continue
				}
				updated = append(updated, p.Name())
				if u != "" {
					latestURL = u
				}
			}
			if len(updated) == 0 {
				if len(errs) > 0 {
					mgmtError(w, http.StatusBadGateway, strings.Join(errs, "; "))
					return
				}
				mgmtError(w, http.StatusBadRequest, "no connected platform supports bot avatar updates")
				return
			}
			resp["avatar_url"] = latestURL
			resp["updated_platforms"] = updated
		}

		if req.Name != "" {
			if len([]rune(req.Name)) > 32 {
				mgmtError(w, http.StatusBadRequest, "name must be 32 characters or fewer")
				return
			}
			var updated []string
			var totalGuilds int
			var anyGlobal bool
			var errs []string
			for _, p := range e.platforms {
				updater, ok := p.(BotNameUpdater)
				if !ok {
					continue
				}
				guilds, globalOK, err := updater.UpdateBotName(r.Context(), req.Name)
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
					continue
				}
				updated = append(updated, p.Name())
				totalGuilds += guilds
				if globalOK {
					anyGlobal = true
				}
			}
			if len(updated) == 0 {
				if len(errs) > 0 {
					mgmtError(w, http.StatusBadGateway, strings.Join(errs, "; "))
					return
				}
				mgmtError(w, http.StatusBadRequest, "no connected platform supports bot name updates")
				return
			}
			resp["name"] = req.Name
			resp["name_updated_platforms"] = updated
			resp["updated_guilds"] = totalGuilds
			resp["global_name_updated"] = anyGlobal
		}

		mgmtJSON(w, http.StatusOK, resp)

	default:
		mgmtError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}
