//go:build darwin && cgo

package discord

import (
	"net/http"
	"testing"
)

func TestEvalDarwinPAC(t *testing.T) {
	script := `function FindProxyForURL(url, host) {
		if (host === "discord.com") {
			return "PROXY 127.0.0.1:7890; DIRECT";
		}
		if (host === "gateway.discord.gg") {
			return "HTTPS 127.0.0.1:8443; DIRECT";
		}
		return "DIRECT";
	}`

	got := evalDarwinPAC(script, "https://discord.com/api/v9/gateway")
	if got == nil || got.String() != "http://127.0.0.1:7890" {
		t.Fatalf("got %v, want http://127.0.0.1:7890", got)
	}

	gotHTTPS := evalDarwinPAC(script, "https://gateway.discord.gg/?v=9")
	if gotHTTPS == nil || gotHTTPS.String() != "https://127.0.0.1:8443" {
		t.Fatalf("got %v, want https://127.0.0.1:8443", gotHTTPS)
	}

	direct := evalDarwinPAC(script, "https://example.com/")
	if direct != nil {
		t.Fatalf("got %v, want nil (DIRECT)", direct)
	}
}

func TestNormalizeProxyLookupURL(t *testing.T) {
	req, _ := http.NewRequest("GET", "wss://gateway.discord.gg/?v=9&encoding=json", nil)
	got := normalizeProxyLookupURL(req.URL)
	if got.Scheme != "https" || got.Host != "gateway.discord.gg" {
		t.Fatalf("got %s, want https://gateway.discord.gg", got.String())
	}
}
