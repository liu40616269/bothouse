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
		for _, p := range e.platforms {
			if _, ok := p.(BotAvatarUpdater); ok {
				platforms = append(platforms, p.Name())
			}
			if avatarURL == "" {
				if g, ok := p.(BotAvatarGetter); ok {
					avatarURL = g.BotAvatarURL()
				}
			}
		}
		mgmtJSON(w, http.StatusOK, map[string]any{
			"supported":  len(platforms) > 0,
			"platforms":  platforms,
			"avatar_url": avatarURL,
		})

	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 12*1024*1024)
		var req struct {
			DataURL string `json:"data_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mgmtError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
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
		mgmtJSON(w, http.StatusOK, map[string]any{
			"avatar_url":        latestURL,
			"updated_platforms": updated,
		})

	default:
		mgmtError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}
