package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// pluginConfig mirrors the plugin's own stanza under plugins.configs.alysis
// in config.yaml. cloud_api_url is the hosted deployment base
// (ALYSIS_CLOUD_API_URL of the CLI: "<host>/functions/v1").
type pluginConfig struct {
	CloudAPIURL string `yaml:"cloud_api_url"`
}

// parsePluginConfig extracts the deployment base from the register payload.
func parsePluginConfig(request []byte) pluginConfig {
	var payload struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if len(request) > 0 {
		_ = json.Unmarshal(request, &payload)
	}
	var cfg pluginConfig
	if len(payload.ConfigYAML) > 0 {
		_ = yaml.Unmarshal(payload.ConfigYAML, &cfg)
	}
	cfg.CloudAPIURL = strings.TrimRight(strings.TrimSpace(cfg.CloudAPIURL), "/")
	return cfg
}

// cloudAPIBase is the deployment API base configured at register time.
var cloudAPIBase string

// gatewayBase derives the LLM gateway base from the cloud API base, matching
// how the CLI positions <base>/llm/v1.
func gatewayBase(cloudBase string) string {
	return strings.TrimRight(cloudBase, "/") + "/llm/v1"
}

// httpPostJSON is swappable so tests can exercise the device flow without a
// network. Production path posts through a plain HTTP client; login is a
// one-time browser-assisted handshake, not model traffic.
var httpPostJSON = func(target string, body any, headers map[string]string) ([]byte, int, error) {
	raw, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return nil, 0, errMarshal
	}
	request, errNew := http.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	if errNew != nil {
		return nil, 0, errNew
	}
	request.Header.Set("Content-Type", "application/json")
	applyCLIWebHeaders(request.Header)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, errDo := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if errDo != nil {
		return nil, 0, errDo
	}
	defer response.Body.Close()
	read, errRead := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return read, response.StatusCode, errRead
}

// httpGetJSON fetches a bearer-authenticated JSON document.
var httpGetJSON = func(target, bearer string) ([]byte, int, error) {
	request, errNew := http.NewRequest(http.MethodGet, target, nil)
	if errNew != nil {
		return nil, 0, errNew
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	applyCLIWebHeaders(request.Header)
	response, errDo := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if errDo != nil {
		return nil, 0, errDo
	}
	defer response.Body.Close()
	read, errRead := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return read, response.StatusCode, errRead
}

// deviceGrant is one pending device login.
type deviceGrant struct {
	DeviceCode string
	UserCode   string
	ExpiresAt  time.Time
}

var (
	grantsMu sync.Mutex
	grants   = map[string]deviceGrant{}
)

func putGrant(state string, grant deviceGrant) {
	grantsMu.Lock()
	defer grantsMu.Unlock()
	grants[state] = grant
}

func takeGrant(state string) (deviceGrant, bool) {
	grantsMu.Lock()
	defer grantsMu.Unlock()
	grant, ok := grants[state]
	return grant, ok
}

func dropGrant(state string) {
	grantsMu.Lock()
	defer grantsMu.Unlock()
	delete(grants, state)
}

func newLoginState() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

// approvalURL ensures the activation page gets the code prefilled, so the webui
// user only has to click Approve.
func approvalURL(verificationURL, userCode string) string {
	target := strings.TrimSpace(verificationURL)
	if userCode == "" {
		return target
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return target
	}
	// verification_url_complete already carries a prefilled code.
	if parsed.Query().Get("code") != "" {
		return target
	}
	separator := "?"
	if parsed.RawQuery != "" {
		separator = "&"
	}
	return target + separator + "code=" + url.QueryEscape(userCode)
}

// hostname mirrors the CLI's platform.node() input to client_name.
func hostname() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "unknown-host"
	}
	return host
}

// installationID is a stable UUID-shaped client identity, mirroring the CLI's
// get_installation_id role in the device-code request (the server treats it as
// an opaque id; a non-UUID string would stand out in hosted account telemetry).
func installationID() string {
	sum := sha1.Sum([]byte("cliproxyapi-alysis:" + hostname()))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// deviceClientName mirrors the CLI's `f"alysis-cli @ {platform.node()}"[:80]`.
func deviceClientName() string {
	name := "alysis-cli @ " + hostname()
	if len(name) > 80 {
		return name[:80]
	}
	return name
}

// deviceGrantResponse mirrors the device-code endpoint payload.
type deviceGrantResponse struct {
	DeviceCode              string  `json:"device_code"`
	UserCode                string  `json:"user_code"`
	VerificationURL         string  `json:"verification_url"`
	VerificationURLComplete string  `json:"verification_url_complete"`
	Interval                float64 `json:"interval"`
	ExpiresIn               float64 `json:"expires_in"`
}

// startLogin opens the hosted device flow and hands the webui a prefilled
// approval link.
func startLogin(_ []byte) (pluginapi.AuthLoginStartResponse, error) {
	base := cloudAPIBase
	if base == "" {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf(
			"cloud_api_url is not configured for the alysis plugin; set it under plugins.configs.alysis",
		)
	}
	body, status, err := httpPostJSON(base+"/device-code", map[string]string{
		"client_name":     deviceClientName(),
		"installation_id": installationID(),
	}, nil)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("device-code request failed: %w", err)
	}
	if status < 200 || status >= 300 {
		return pluginapi.AuthLoginStartResponse{}, upstreamError(status, body)
	}
	var grant deviceGrantResponse
	if err := json.Unmarshal(body, &grant); err != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("device-code response invalid: %w", err)
	}
	if grant.DeviceCode == "" || grant.UserCode == "" {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("login service did not return a device code")
	}
	expiresIn := 15 * time.Minute
	if grant.ExpiresIn > 0 {
		expiresIn = time.Duration(grant.ExpiresIn * float64(time.Second))
	}
	verification := grant.VerificationURLComplete
	if verification == "" {
		verification = grant.VerificationURL
	}
	state := newLoginState()
	putGrant(state, deviceGrant{
		DeviceCode: grant.DeviceCode,
		UserCode:   grant.UserCode,
		ExpiresAt:  time.Now().Add(expiresIn),
	})
	return pluginapi.AuthLoginStartResponse{
		Provider:  providerID,
		URL:       approvalURL(verification, grant.UserCode),
		State:     state,
		ExpiresAt: time.Now().Add(expiresIn),
		Metadata:  map[string]any{"user_code": grant.UserCode},
	}, nil
}

// authLoginPollRequest mirrors the poll wire shape.
type authLoginPollRequest struct {
	State string `json:"State"`
}

// pollLogin checks one device-code approval cycle and, once approved, fetches
// the account label and assembles the persisted auth JSON.
func pollLogin(request []byte) (pluginapi.AuthLoginPollResponse, error) {
	var req authLoginPollRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	base := cloudAPIBase
	grant, ok := takeGrant(req.State)
	if base == "" || !ok {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
			Message: "this login session expired; start the login again"}, nil
	}
	if time.Now().After(grant.ExpiresAt) {
		dropGrant(req.State)
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
			Message: "this login code expired; start the login again"}, nil
	}

	body, status, err := httpPostJSON(base+"/device-token", map[string]string{
		"device_code": grant.DeviceCode,
	}, nil)
	if err != nil {
		return pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for approval",
		}, nil
	}
	if status < 200 || status >= 300 {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
			Message: upstreamMessage(status, body)}, nil
	}
	var token struct {
		Status string `json:"status"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
			Message: fmt.Sprintf("device-token response invalid: %v", err)}, nil
	}

	switch strings.TrimSpace(strings.ToLower(token.Status)) {
	case "approved":
		if strings.TrimSpace(token.Key) == "" {
			return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
				Message: "login service did not return a key"}, nil
		}
		dropGrant(req.State)
		authData, errBuild := buildLoginAuth(base, token.Key)
		if errBuild != nil {
			return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
				Message: errBuild.Error()}, nil
		}
		return pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusSuccess,
			Message: "Alysis login approved",
			Auth:    authData,
		}, nil
	case "denied":
		dropGrant(req.State)
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
			Message: "login was rejected on the website"}, nil
	case "expired", "not_found", "already_claimed":
		dropGrant(req.State)
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError,
			Message: "this login code expired; start the login again"}, nil
	default: // pending
		return pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for approval at " + approvalURL(cloudSiteURL, grant.UserCode),
		}, nil
	}
}

// cloudSiteURL is the product site serving the activation page.
const cloudSiteURL = "https://alysiscode.com/activate"

// buildLoginAuth reads the account label and builds the auth record the host
// persists into the auth directory.
func buildLoginAuth(base, key string) (pluginapi.AuthData, error) {
	storage := map[string]string{
		"type":     providerID,
		"api_key":  key,
		"base_url": gatewayBase(base),
	}
	label := key
	if body, status, err := httpGetJSON(base+"/account-status", key); err == nil && status >= 200 && status < 300 {
		var account struct {
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
		}
		if json.Unmarshal(body, &account) == nil {
			for _, candidate := range []string{account.Email, account.DisplayName} {
				if trimmed := strings.TrimSpace(candidate); trimmed != "" {
					label = trimmed
					storage["email"] = trimmed
					break
				}
			}
		}
	}
	suffix := url.PathEscape(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, label))
	if suffix == "" {
		return pluginapi.AuthData{}, fmt.Errorf("login record label is empty")
	}
	fileName := providerID + "-" + suffix + ".json"
	email := strings.TrimSpace(storage["email"])
	if email == "" {
		delete(storage, "email")
	} else {
		label = email
	}
	raw, errMarshal := json.MarshalIndent(storage, "", "  ")
	if errMarshal != nil {
		return pluginapi.AuthData{}, errMarshal
	}
	fileLabel := strings.TrimSuffix(fileName, ".json")
	return pluginapi.AuthData{
		Provider:         providerID,
		ID:               providerID + ":" + fileLabel,
		FileName:         fileName,
		Label:            label,
		StorageJSON:      append(raw, '\n'),
		Metadata:         map[string]any{"type": providerID, "email": storage["email"]},
		Attributes:       map[string]string{"base_url": storage["base_url"]},
		Disabled:         false,
		NextRefreshAfter: time.Now().Add(24 * time.Hour),
	}, nil
}
