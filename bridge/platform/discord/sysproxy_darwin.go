//go:build darwin && cgo

package discord

/*
#cgo LDFLAGS: -framework CFNetwork -framework CoreFoundation -framework JavaScriptCore
#include <CFNetwork/CFNetwork.h>
#include <CoreFoundation/CoreFoundation.h>
#include <JavaScriptCore/JavaScriptCore.h>
#include <string.h>

static void copyCFStringToBuf(CFStringRef str, char *buf, size_t cap) {
	if (!buf || cap == 0) return;
	buf[0] = '\0';
	if (!str) return;
	CFStringGetCString(str, buf, (CFIndex)cap, kCFStringEncodingUTF8);
}

static int extractProxyEntry(CFDictionaryRef dict,
	char *outType, size_t typeCap,
	char *outHost, size_t hostCap,
	int *outPort,
	char *outPAC, size_t pacCap) {
	if (!dict) return 0;
	CFStringRef ptype = (CFStringRef)CFDictionaryGetValue(dict, kCFProxyTypeKey);
	if (!ptype) return 0;

	if (CFEqual(ptype, kCFProxyTypeNone)) {
		strncpy(outType, "direct", typeCap - 1);
		outType[typeCap - 1] = '\0';
		return 1;
	}
	if (CFEqual(ptype, kCFProxyTypeHTTP) || CFEqual(ptype, kCFProxyTypeHTTPS)) {
		strncpy(outType, "http", typeCap - 1);
		outType[typeCap - 1] = '\0';
		CFStringRef host = (CFStringRef)CFDictionaryGetValue(dict, kCFProxyHostNameKey);
		CFNumberRef port = (CFNumberRef)CFDictionaryGetValue(dict, kCFProxyPortNumberKey);
		copyCFStringToBuf(host, outHost, hostCap);
		int p = 0;
		if (port) CFNumberGetValue(port, kCFNumberIntType, &p);
		*outPort = p;
		return 1;
	}
	if (CFEqual(ptype, kCFProxyTypeSOCKS)) {
		strncpy(outType, "socks5", typeCap - 1);
		outType[typeCap - 1] = '\0';
		CFStringRef host = (CFStringRef)CFDictionaryGetValue(dict, kCFProxyHostNameKey);
		CFNumberRef port = (CFNumberRef)CFDictionaryGetValue(dict, kCFProxyPortNumberKey);
		copyCFStringToBuf(host, outHost, hostCap);
		int p = 0;
		if (port) CFNumberGetValue(port, kCFNumberIntType, &p);
		*outPort = p;
		return 1;
	}
	if (outPAC && pacCap > 0 && CFEqual(ptype, kCFProxyTypeAutoConfigurationURL)) {
		strncpy(outType, "pac", typeCap - 1);
		outType[typeCap - 1] = '\0';
		CFTypeRef pacVal = CFDictionaryGetValue(dict, kCFProxyAutoConfigurationURLKey);
		if (pacVal) {
			if (CFGetTypeID(pacVal) == CFURLGetTypeID()) {
				copyCFStringToBuf(CFURLGetString((CFURLRef)pacVal), outPAC, pacCap);
			} else if (CFGetTypeID(pacVal) == CFStringGetTypeID()) {
				copyCFStringToBuf((CFStringRef)pacVal, outPAC, pacCap);
			}
		}
		return 1;
	}
	if (outPAC && pacCap > 0 && CFEqual(ptype, kCFProxyTypeAutoConfigurationJavaScript)) {
		strncpy(outType, "pacjs", typeCap - 1);
		outType[typeCap - 1] = '\0';
		CFStringRef js = (CFStringRef)CFDictionaryGetValue(dict, kCFProxyAutoConfigurationJavaScriptKey);
		copyCFStringToBuf(js, outPAC, pacCap);
		return 1;
	}
	return 0;
}

static int resolveSystemProxyForURL(const char *rawURL,
	char *outType, size_t typeCap,
	char *outHost, size_t hostCap,
	int *outPort,
	char *outPAC, size_t pacCap) {
	outType[0] = '\0';
	outHost[0] = '\0';
	*outPort = 0;
	if (outPAC && pacCap > 0) outPAC[0] = '\0';

	CFDictionaryRef settings = CFNetworkCopySystemProxySettings();
	if (!settings) return 0;

	CFStringRef urlStr = CFStringCreateWithCString(kCFAllocatorDefault, rawURL, kCFStringEncodingUTF8);
	if (!urlStr) {
		CFRelease(settings);
		return 0;
	}
	CFURLRef cfURL = CFURLCreateWithString(kCFAllocatorDefault, urlStr, NULL);
	CFRelease(urlStr);
	if (!cfURL) {
		CFRelease(settings);
		return 0;
	}

	CFArrayRef proxies = CFNetworkCopyProxiesForURL(cfURL, settings);
	CFRelease(cfURL);
	CFRelease(settings);
	if (!proxies) return 0;

	int found = 0;
	CFIndex count = CFArrayGetCount(proxies);
	for (CFIndex i = 0; i < count; i++) {
		CFDictionaryRef dict = (CFDictionaryRef)CFArrayGetValueAtIndex(proxies, i);
		if (extractProxyEntry(dict, outType, typeCap, outHost, hostCap, outPort, outPAC, pacCap)) {
			found = 1;
			break;
		}
	}
	CFRelease(proxies);
	return found;
}

static int evalJSString(const char *jsCode, char *outBuf, size_t outCap) {
	if (!jsCode || !outBuf || outCap == 0) return 0;
	outBuf[0] = '\0';
	JSGlobalContextRef ctx = JSGlobalContextCreate(NULL);
	if (!ctx) return 0;

	JSStringRef script = JSStringCreateWithUTF8CString(jsCode);
	JSValueRef exception = NULL;
	JSValueRef result = JSEvaluateScript(ctx, script, NULL, NULL, 1, &exception);
	JSStringRelease(script);

	int ok = 0;
	if (!exception && result && !JSValueIsUndefined(ctx, result) && !JSValueIsNull(ctx, result)) {
		JSStringRef resStr = JSValueToStringCopy(ctx, result, &exception);
		if (resStr) {
			JSStringGetUTF8CString(resStr, outBuf, outCap);
			JSStringRelease(resStr);
			ok = 1;
		}
	}
	JSGlobalContextRelease(ctx);
	return ok;
}
*/
import "C"

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	sysProxyCacheTTL = 60 * time.Second
	pacScriptMaxSize = 1 << 20 // 1 MiB
)

const pacHelpersJS = `
function isPlainHostName(host) { return host.indexOf('.') === -1; }
function dnsDomainIs(host, domain) {
	return host.length >= domain.length && host.substring(host.length - domain.length) === domain;
}
function localHostOrDomainIs(host, hostdom) {
	return host === hostdom || hostdom.lastIndexOf(host + '.', 0) === 0;
}
function dnsDomainLevels(host) { return host.split('.').length - 1; }
function shExpMatch(str, shexp) {
	var re = shexp.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.');
	return new RegExp('^' + re + '$').test(str);
}
function _ipToInt(ip) {
	var p = String(ip).split('.');
	if (p.length !== 4) return -1;
	return ((+p[0] << 24) | (+p[1] << 16) | (+p[2] << 8) | (+p[3])) >>> 0;
}
function isInNet(host, pattern, mask) {
	var h = _ipToInt(host), p = _ipToInt(pattern), m = _ipToInt(mask);
	if (h < 0 || p < 0 || m < 0) return false;
	return (h & m) === (p & m);
}
function isResolvable(host) { return true; }
function dnsResolve(host) { return host; }
function myIpAddress() { return "127.0.0.1"; }
`

type cachedProxyEntry struct {
	proxy     *url.URL
	expiresAt time.Time
}

type cachedPACScript struct {
	script    string
	expiresAt time.Time
}

var (
	sysProxyMu    sync.Mutex
	sysProxyCache = map[string]cachedProxyEntry{}
	pacCache      = map[string]cachedPACScript{}
	pacHTTPClient = &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   5 * time.Second,
	}
)

// systemProxyFunc resolves the proxy for an HTTP or WebSocket request:
// 1. Standard environment variables (HTTPS_PROXY / HTTP_PROXY / NO_PROXY) take precedence.
// 2. macOS system proxy settings (static HTTP/HTTPS/SOCKS or PAC script via CFNetwork + JavaScriptCore).
func systemProxyFunc(req *http.Request) (*url.URL, error) {
	if req == nil || req.URL == nil {
		return nil, nil
	}
	if envProxy, err := http.ProxyFromEnvironment(req); envProxy != nil || err != nil {
		return envProxy, err
	}
	host := req.URL.Hostname()
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil, nil
	}

	targetURL := normalizeProxyLookupURL(req.URL)
	cacheKey := targetURL.Scheme + "://" + targetURL.Host
	now := time.Now()

	sysProxyMu.Lock()
	if ent, ok := sysProxyCache[cacheKey]; ok && now.Before(ent.expiresAt) {
		sysProxyMu.Unlock()
		return ent.proxy, nil
	}
	sysProxyMu.Unlock()

	resolved := resolveDarwinSystemProxy(targetURL.String())

	sysProxyMu.Lock()
	sysProxyCache[cacheKey] = cachedProxyEntry{
		proxy:     resolved,
		expiresAt: now.Add(sysProxyCacheTTL),
	}
	sysProxyMu.Unlock()

	return resolved, nil
}

func normalizeProxyLookupURL(u *url.URL) *url.URL {
	clone := *u
	switch strings.ToLower(clone.Scheme) {
	case "wss":
		clone.Scheme = "https"
	case "ws":
		clone.Scheme = "http"
	case "":
		clone.Scheme = "https"
	}
	return &clone
}

func resolveDarwinSystemProxy(rawURL string) *url.URL {
	cURL := C.CString(rawURL)
	defer C.free(unsafe.Pointer(cURL))

	var typeBuf [32]C.char
	var hostBuf [256]C.char
	var pacBuf [4096]C.char
	var port C.int

	ok := C.resolveSystemProxyForURL(
		cURL,
		&typeBuf[0], C.size_t(len(typeBuf)),
		&hostBuf[0], C.size_t(len(hostBuf)),
		&port,
		&pacBuf[0], C.size_t(len(pacBuf)),
	)
	if ok == 0 {
		return nil
	}

	ptype := C.GoString(&typeBuf[0])
	switch ptype {
	case "http", "https", "socks5":
		host := strings.TrimSpace(C.GoString(&hostBuf[0]))
		if host == "" || port <= 0 {
			return nil
		}
		return &url.URL{Scheme: ptype, Host: net.JoinHostPort(host, strconv.Itoa(int(port)))}
	case "pac":
		pacURL := strings.TrimSpace(C.GoString(&pacBuf[0]))
		if pacURL == "" {
			return nil
		}
		script := fetchPACScript(pacURL)
		if script == "" {
			return nil
		}
		return evalDarwinPAC(script, rawURL)
	case "pacjs":
		script := C.GoString(&pacBuf[0])
		if strings.TrimSpace(script) == "" {
			return nil
		}
		return evalDarwinPAC(script, rawURL)
	default:
		return nil
	}
}

func fetchPACScript(pacURL string) string {
	now := time.Now()
	sysProxyMu.Lock()
	if ent, ok := pacCache[pacURL]; ok && now.Before(ent.expiresAt) {
		sysProxyMu.Unlock()
		return ent.script
	}
	sysProxyMu.Unlock()

	resp, err := pacHTTPClient.Get(pacURL)
	if err != nil {
		slog.Debug("discord: fetch macOS PAC failed", "error", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, pacScriptMaxSize))
	if err != nil {
		return ""
	}
	script := string(body)

	sysProxyMu.Lock()
	pacCache[pacURL] = cachedPACScript{
		script:    script,
		expiresAt: now.Add(sysProxyCacheTTL),
	}
	sysProxyMu.Unlock()
	return script
}

func evalDarwinPAC(script, rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	urlJSON, _ := json.Marshal(rawURL)
	hostJSON, _ := json.Marshal(u.Hostname())
	js := pacHelpersJS + "\n" + script + "\nFindProxyForURL(" + string(urlJSON) + ", " + string(hostJSON) + ");"

	cJS := C.CString(js)
	defer C.free(unsafe.Pointer(cJS))
	var outBuf [1024]C.char
	if C.evalJSString(cJS, &outBuf[0], C.size_t(len(outBuf))) == 0 {
		return nil
	}
	return parsePACResult(C.GoString(&outBuf[0]))
}

func parsePACResult(res string) *url.URL {
	for _, entry := range strings.Split(res, ";") {
		fields := strings.Fields(strings.TrimSpace(entry))
		if len(fields) == 0 {
			continue
		}
		directive := strings.ToUpper(fields[0])
		if directive == "DIRECT" {
			return nil
		}
		if len(fields) < 2 {
			continue
		}
		hostPort := fields[1]
		switch directive {
		case "PROXY", "HTTP":
			return &url.URL{Scheme: "http", Host: hostPort}
		case "HTTPS":
			return &url.URL{Scheme: "https", Host: hostPort}
		case "SOCKS", "SOCKS4", "SOCKS5":
			return &url.URL{Scheme: "socks5", Host: hostPort}
		}
	}
	return nil
}
