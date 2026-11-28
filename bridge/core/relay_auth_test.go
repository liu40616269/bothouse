package core

import "testing"

func TestDecodeOctalUTF8(t *testing.T) {
	in := `\345\246\202\346\234\211\347\231\273\345\275\225\351\227\256\351\242\230`
	got := decodeOctalUTF8(in)
	want := "如有登录问题"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCleanRelayAuthOutputAndURLs(t *testing.T) {
	raw := "\x1b[32m\\350\\257\\267\\350\\256\\277\\351\\227\\256: https://auth.example.com/verify?id=123\x1b[0m\r\nPassword: "
	clean := cleanRelayAuthOutput(raw)
	if clean != "请访问: https://auth.example.com/verify?id=123\nPassword:" {
		t.Fatalf("unexpected clean output: %q", clean)
	}
	urls := extractRelayAuthURLs(clean)
	if len(urls) != 1 || urls[0] != "https://auth.example.com/verify?id=123" {
		t.Fatalf("unexpected urls: %v", urls)
	}
}
