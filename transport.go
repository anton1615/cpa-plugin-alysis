package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// applyCLIWebHeaders mimics stdlib urllib defaults. The official CLI calls the
// cloud functions and gateway /models through urllib.request (not httpx), so
// those requests carry this exact header trio.
func applyCLIWebHeaders(h http.Header) {
	h.Set("User-Agent", "python-urllib/3.13")
	h.Set("Accept-Encoding", "identity")
	h.Set("Connection", "close")
}

// upstreamClient builds the HTTP client used for all gateway traffic (model
// discovery and executor calls). Transports are cached per proxy setting.
//
// shortcut: host.http.do executes with newHTTPClient(nil) on the host side, so
// the host-wide requests.proxy-url — never the auth file's proxy_url — decides
// the route for plugin callbacks; upgrade to it if the host ever plumbs the
// per-auth proxy into the ABI http callbacks.
func upstreamClient(proxyURL string) (*http.Client, error) {
	transport, err := transportFor(proxyURL)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport}, nil
}

var (
	transportCacheMu sync.Mutex
	transportCache   = map[string]*http.Transport{}
)

func transportFor(proxyURL string) (*http.Transport, error) {
	setting := strings.TrimSpace(proxyURL)
	if setting == "" || setting == "direct" {
		return defaultTransport(nil), nil
	}
	transportCacheMu.Lock()
	cached, ok := transportCache[setting]
	transportCacheMu.Unlock()
	if ok {
		return cached, nil
	}
	parsed, err := url.Parse(setting)
	if err != nil {
		return nil, fmt.Errorf("auth proxy_url: %w", err)
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	custom := defaultTransport(dialer)
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		custom.Proxy = http.ProxyURL(parsed)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: parsed.User.Username(), Password: password}
		}
		socksDialer, errDial := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
		if errDial != nil {
			return nil, fmt.Errorf("auth proxy_url: %w", errDial)
		}
		contextDialer, ok := socksDialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("auth proxy_url: socks dialer lacks context support")
		}
		custom.DialContext = contextDialer.DialContext
	default:
		return nil, fmt.Errorf("auth proxy_url: unsupported scheme %q", parsed.Scheme)
	}
	transportCacheMu.Lock()
	transportCache[setting] = custom
	transportCacheMu.Unlock()
	return custom, nil
}

func defaultTransport(dialer *net.Dialer) *http.Transport {
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	}
	// HTTP/1.1 only: the official CLI's httpx client never negotiates h2
	// (http2=False default), so ALPN h2 would be a client fingerprint.
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     false,
		TLSClientConfig:       &tls.Config{NextProtos: []string{"http/1.1"}},
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
