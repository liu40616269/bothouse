package discord

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// newWebSocketDialer builds a gorilla/websocket Dialer that supports http://,
// socks5://, AND https:// (TLS + HTTP CONNECT) proxies. gorilla/websocket v1.5
// only registers an "http" proxy dialer and fails with "unknown scheme: https".
func newWebSocketDialer(proxyFn func(*http.Request) (*url.URL, error)) *websocket.Dialer {
	baseDialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
		Proxy: func(req *http.Request) (*url.URL, error) {
			if proxyFn == nil {
				return nil, nil
			}
			u, err := proxyFn(req)
			if err != nil || u == nil {
				return u, err
			}
			if strings.EqualFold(u.Scheme, "https") {
				// Handled in NetDialContext below.
				return nil, nil
			}
			return u, nil
		},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if proxyFn != nil {
				probeReq := &http.Request{URL: &url.URL{Scheme: "https", Host: addr}}
				if proxyURL, err := proxyFn(probeReq); err == nil && proxyURL != nil && strings.EqualFold(proxyURL.Scheme, "https") {
					return dialViaHTTPSProxy(ctx, baseDialer, proxyURL, addr)
				}
			}
			return baseDialer.DialContext(ctx, network, addr)
		},
	}
}

func dialViaHTTPSProxy(ctx context.Context, d *net.Dialer, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	proxyHost := proxyURL.Host
	if _, _, err := net.SplitHostPort(proxyHost); err != nil {
		proxyHost = net.JoinHostPort(proxyHost, "443")
	}
	rawConn, err := d.DialContext(ctx, "tcp", proxyHost)
	if err != nil {
		return nil, err
	}
	serverName := proxyURL.Hostname()
	tlsConn := tls.Client(rawConn, &tls.Config{ServerName: serverName})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, err
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: targetAddr},
		Host:   targetAddr,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		cred := proxyURL.User.Username()
		if pass, ok := proxyURL.User.Password(); ok {
			cred += ":" + pass
		}
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred)))
	}
	if err := req.Write(tlsConn); err != nil {
		tlsConn.Close()
		return nil, err
	}
	br := bufio.NewReader(tlsConn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tlsConn.Close()
		return nil, fmt.Errorf("https proxy CONNECT %s: %s", targetAddr, resp.Status)
	}
	return tlsConn, nil
}
