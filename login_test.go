package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func stubHTTP(handler func(target string, body []byte) (int, string)) func() {
	previous := httpPostJSON
	httpPostJSON = func(target string, body any, headers map[string]string) ([]byte, int, error) {
		raw, _ := json.Marshal(body)
		status, reply := handler(target, raw)
		return []byte(reply), status, nil
	}
	return func() { httpPostJSON = previous }
}

func TestParsePluginConfigReadsCloudAPIURL(t *testing.T) {
	request, _ := json.Marshal(struct {
		ConfigYAML    []byte `json:"config_yaml"`
		SchemaVersion uint32 `json:"schema_version"`
	}{ConfigYAML: []byte("cloud_api_url: \"https://example.test/functions/v1///\"\n")})
	if got := parsePluginConfig(request); got.CloudAPIURL != "https://example.test/functions/v1" {
		t.Fatalf("cloud_api_url = %q", got.CloudAPIURL)
	}
	if got := parsePluginConfig(nil); got.CloudAPIURL != "" {
		t.Fatalf("empty request should yield empty base, got %q", got.CloudAPIURL)
	}
}

func TestGatewayBaseAppendsLLMPath(t *testing.T) {
	if got := gatewayBase("https://x.test/functions/v1"); got != "https://x.test/functions/v1/llm/v1" {
		t.Fatalf("gateway base = %q", got)
	}
}

func TestApprovalURLPrefillsCode(t *testing.T) {
	if got := approvalURL("https://alysiscode.com/activate", "AB12"); got != "https://alysiscode.com/activate?code=AB12" {
		t.Fatalf("plain url = %q", got)
	}
	if got := approvalURL("https://alysiscode.com/activate?x=1", "AB12"); got != "https://alysiscode.com/activate?x=1&code=AB12" {
		t.Fatalf("url with query = %q", got)
	}
	// shortcut: user codes arrive from the server, escaping is belt and braces
	if got := approvalURL("https://alysiscode.com/activate", ""); got != "https://alysiscode.com/activate" {
		t.Fatalf("empty code should keep url, got %q", got)
	}
}

func TestStartLoginWithoutConfigFails(t *testing.T) {
	saved := cloudAPIBase
	cloudAPIBase = ""
	defer func() { cloudAPIBase = saved }()
	if _, err := startLogin(nil); err == nil || !strings.Contains(err.Error(), "cloud_api_url") {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestStartLoginOpensDeviceFlow(t *testing.T) {
	saved := cloudAPIBase
	cloudAPIBase = "https://example.test/functions/v1"
	defer func() { cloudAPIBase = saved }()

	restore := stubHTTP(func(target string, body []byte) (int, string) {
		if target != "https://example.test/functions/v1/device-code" {
			t.Errorf("unexpected device-code target %q", target)
		}
		return 200, `{"device_code":"dc_1","user_code":"AB12","verification_url":"https://alysiscode.com/activate","expires_in":900}`
	})
	defer restore()

	resp, err := startLogin(nil)
	if err != nil {
		t.Fatalf("startLogin: %v", err)
	}
	if resp.Provider != providerID || resp.State == "" {
		t.Fatalf("provider/state not populated: %+v", resp)
	}
	if resp.URL != "https://alysiscode.com/activate?code=AB12" {
		t.Fatalf("url = %q", resp.URL)
	}
	grant, ok := takeGrant(resp.State)
	if !ok || grant.DeviceCode != "dc_1" || grant.UserCode != "AB12" {
		t.Fatalf("grant not stored: %+v %v", grant, ok)
	}
	dropGrant(resp.State)
}

func loginFixture(t *testing.T) (state string, restore func(), restoreBase func()) {
	t.Helper()
	savedBase := cloudAPIBase
	cloudAPIBase = "https://example.test/functions/v1"
	state = newLoginState()
	putGrant(state, deviceGrant{DeviceCode: "dc_1", UserCode: "AB12", ExpiresAt: nowPlusMinute()})
	restore = stubHTTP(func(target string, body []byte) (int, string) {
		if target != "https://example.test/functions/v1/device-token" {
			t.Errorf("unexpected device-token target %q", target)
		}
		return 200, `{"status":"pending"}`
	})
	restoreBase = func() { cloudAPIBase = savedBase }
	return state, restore, restoreBase
}

func TestPollLoginPendingKeepsGrant(t *testing.T) {
	state, restore, restoreBase := loginFixture(t)
	defer restoreBase()
	defer restore()

	resp, err := pollLogin(mustJSON(t, map[string]string{"State": state}))
	if err != nil {
		t.Fatalf("pollLogin: %v", err)
	}
	if resp.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("status = %q", resp.Status)
	}
	if _, ok := takeGrant(state); !ok {
		t.Fatal("pending poll dropped the grant")
	}
	dropGrant(state)
}

func TestPollLoginDeniedDropsGrant(t *testing.T) {
	state, restore, restoreBase := loginFixture(t)
	defer restoreBase()

	httpPostJSON = func(target string, body any, headers map[string]string) ([]byte, int, error) {
		return []byte(`{"status":"denied"}`), 200, nil
	}
	defer restore()

	resp, err := pollLogin(mustJSON(t, map[string]string{"State": state}))
	if err != nil {
		t.Fatalf("pollLogin: %v", err)
	}
	if resp.Status != pluginapi.AuthLoginStatusError || !strings.Contains(resp.Message, "rejected") {
		t.Fatalf("denied response = %+v", resp)
	}
	if _, ok := takeGrant(state); ok {
		t.Fatal("denied poll kept the grant")
	}
}

func TestPollLoginApprovedBuildsAuthRecord(t *testing.T) {
	state, restore, restoreBase := loginFixture(t)
	defer restoreBase()

	httpPostJSON = func(target string, body any, headers map[string]string) ([]byte, int, error) {
		return []byte(`{"status":"approved","key":"slk_new"}`), 200, nil
	}
	defer restore()
	previousGet := httpGetJSON
	httpGetJSON = func(target, bearer string) ([]byte, int, error) {
		if target != "https://example.test/functions/v1/account-status" || bearer != "slk_new" {
			t.Errorf("unexpected account-status call %q %q", target, bearer)
		}
		return []byte(`{"email":"user@example.test"}`), 200, nil
	}
	defer func() { httpGetJSON = previousGet }()

	resp, err := pollLogin(mustJSON(t, map[string]string{"State": state}))
	if err != nil {
		t.Fatalf("pollLogin: %v", err)
	}
	if resp.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("status = %q message = %q", resp.Status, resp.Message)
	}
	auth := resp.Auth
	if auth.Provider != providerID || auth.Label != "user@example.test" {
		t.Fatalf("auth provider/label = %q %q", auth.Provider, auth.Label)
	}
	if auth.FileName != "alysis-user-example.test.json" {
		t.Fatalf("file name = %q", auth.FileName)
	}
	if auth.ID != "alysis:alysis-user-example.test" {
		t.Fatalf("id = %q", auth.ID)
	}
	if auth.Attributes["base_url"] != "https://example.test/functions/v1/llm/v1" {
		t.Fatalf("base_url attribute = %q", auth.Attributes["base_url"])
	}
	storage, err := parseAuthStorage(auth.StorageJSON)
	if err != nil {
		t.Fatalf("persisted storage does not parse: %v", err)
	}
	if storage.APIKey != "slk_new" {
		t.Fatalf("persisted key = %q", storage.APIKey)
	}
	if storage.Email != "user@example.test" {
		t.Fatalf("persisted email = %q", storage.Email)
	}
	if _, ok := takeGrant(state); ok {
		t.Fatal("approved poll kept the grant")
	}
}

func TestPollLoginExpiredCodeReportsRestart(t *testing.T) {
	state, restore, restoreBase := loginFixture(t)
	defer restoreBase()

	httpPostJSON = func(target string, body any, headers map[string]string) ([]byte, int, error) {
		return []byte(`{"status":"expired"}`), 200, nil
	}
	defer restore()

	resp, err := pollLogin(mustJSON(t, map[string]string{"State": state}))
	if err != nil {
		t.Fatalf("pollLogin: %v", err)
	}
	if resp.Status != pluginapi.AuthLoginStatusError || !strings.Contains(resp.Message, "expired") {
		t.Fatalf("expired response = %+v", resp)
	}
}

func TestPollLoginUnknownStateFails(t *testing.T) {
	resp, err := pollLogin(mustJSON(t, map[string]string{"State": "nope"}))
	if err != nil {
		t.Fatalf("pollLogin: %v", err)
	}
	if resp.Status != pluginapi.AuthLoginStatusError {
		t.Fatalf("status = %q", resp.Status)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func nowPlusMinute() time.Time { return time.Now().Add(time.Minute) }
