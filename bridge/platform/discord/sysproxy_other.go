//go:build !darwin || !cgo

package discord

import (
	"net/http"
	"net/url"
)

func systemProxyFunc(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}
