package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockAvatarPlatform struct {
	avatarURL string
	username  string
	lastMIME  string
	lastBytes []byte
	lastName  string
}

func (m *mockAvatarPlatform) Name() string                             { return "discord" }
func (m *mockAvatarPlatform) Start(MessageHandler) error               { return nil }
func (m *mockAvatarPlatform) Reply(context.Context, any, string) error { return nil }
func (m *mockAvatarPlatform) Send(context.Context, any, string) error  { return nil }
func (m *mockAvatarPlatform) Stop() error                              { return nil }
func (m *mockAvatarPlatform) BotAvatarURL() string                     { return m.avatarURL }
func (m *mockAvatarPlatform) BotUsername() string                      { return m.username }
func (m *mockAvatarPlatform) UpdateBotAvatar(_ context.Context, mime string, b []byte) (string, error) {
	m.lastMIME = mime
	m.lastBytes = append([]byte(nil), b...)
	m.avatarURL = "https://cdn.discordapp.com/avatars/123/updated.png"
	return m.avatarURL, nil
}
func (m *mockAvatarPlatform) UpdateBotName(_ context.Context, name string) (int, bool, error) {
	m.lastName = name
	m.username = name
	return 1, true, nil
}

func TestHandleProjectAvatar(t *testing.T) {
	plat := &mockAvatarPlatform{
		avatarURL: "https://cdn.discordapp.com/avatars/123/old.png",
		username:  "oldbot",
	}
	eng := &Engine{name: "gpt", platforms: []Platform{plat}}
	srv := &ManagementServer{}

	// GET current avatar & username
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/gpt/avatar", nil)
	rec := httptest.NewRecorder()
	srv.handleProjectAvatar(rec, req, eng)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var getResp struct {
		Data struct {
			Supported    bool     `json:"supported"`
			SupportsName bool     `json:"supports_name"`
			AvatarURL    string   `json:"avatar_url"`
			BotUsername  string   `json:"bot_username"`
			Platforms    []string `json:"platforms"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatal(err)
	}
	if !getResp.Data.Supported || !getResp.Data.SupportsName || getResp.Data.AvatarURL != plat.avatarURL || getResp.Data.BotUsername != "oldbot" {
		t.Fatalf("unexpected GET response: %+v", getResp.Data)
	}

	// POST new PNG avatar (minimal valid PNG header)
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	body, _ := json.Marshal(map[string]string{
		"data_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader),
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/gpt/avatar", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	srv.handleProjectAvatar(rec, req, eng)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST avatar expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if plat.lastMIME != "image/png" || !bytes.Equal(plat.lastBytes, pngHeader) {
		t.Fatalf("unexpected platform call: mime=%q len=%d", plat.lastMIME, len(plat.lastBytes))
	}

	// POST new bot name
	nameBody, _ := json.Marshal(map[string]string{"name": "新名字"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/gpt/avatar", bytes.NewReader(nameBody))
	rec = httptest.NewRecorder()
	srv.handleProjectAvatar(rec, req, eng)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST name expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if plat.lastName != "新名字" {
		t.Fatalf("expected UpdateBotName('新名字'), got %q", plat.lastName)
	}
}
