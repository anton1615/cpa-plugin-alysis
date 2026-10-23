package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAuthStorageAcceptsAlysisFile(t *testing.T) {
	raw := []byte(`{"type":"alysis","api_key":"slk_test","base_url":"https://example.test/functions/v1/llm/v1","email":"a@b.c"}`)
	storage, err := parseAuthStorage(raw)
	if err != nil {
		t.Fatalf("parseAuthStorage returned error: %v", err)
	}
	if storage.APIKey != "slk_test" {
		t.Fatalf("api_key = %q", storage.APIKey)
	}
	if storage.BaseURL != "https://example.test/functions/v1/llm/v1" {
		t.Fatalf("base_url = %q", storage.BaseURL)
	}
	if storage.label() != "a@b.c" {
		t.Fatalf("label = %q", storage.label())
	}
}

func TestParseAuthStorageTrimsTrailingSlash(t *testing.T) {
	storage, err := parseAuthStorage([]byte(`{"type":"ALYSIS","api_key":" k ","base_url":"https://x.test/v1///"}`))
	if err != nil {
		t.Fatalf("parseAuthStorage returned error: %v", err)
	}
	if storage.BaseURL != "https://x.test/v1" {
		t.Fatalf("base_url = %q", storage.BaseURL)
	}
	if storage.APIKey != "k" {
		t.Fatalf("api_key = %q", storage.APIKey)
	}
}

func TestParseAuthStorageRejectsInvalidFiles(t *testing.T) {
	cases := map[string]string{
		"empty":        ``,
		"not json":     `{`,
		"wrong type":   `{"type":"codex","api_key":"k","base_url":"https://x.test"}`,
		"missing key":  `{"type":"alysis","base_url":"https://x.test"}`,
		"missing base": `{"type":"alysis","api_key":"k"}`,
	}
	for name, raw := range cases {
		if _, err := parseAuthStorage([]byte(raw)); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestParseAuthMarksForeignFilesUnhandled(t *testing.T) {
	request, _ := json.Marshal(authParseRequest{
		FileName: "codex-account.json",
		RawJSON:  json.RawMessage(`{"type":"codex","access_token":"x"}`),
	})
	response := parseAuth(request)
	if response.Handled {
		t.Fatal("foreign auth file must not be handled")
	}
}

func TestParseAuthBuildsHandledAuth(t *testing.T) {
	request, _ := json.Marshal(authParseRequest{
		FileName: "alysis-main.json",
		RawJSON:  json.RawMessage(`{"type":"alysis","api_key":"slk_x","base_url":"https://x.test/v1","email":"a@b.c"}`),
	})
	response := parseAuth(request)
	if !response.Handled {
		t.Fatal("alysis auth file must be handled")
	}
	if response.Auth.Provider != providerID {
		t.Fatalf("provider = %q", response.Auth.Provider)
	}
	if response.Auth.ID != "alysis:alysis-main" {
		t.Fatalf("id = %q", response.Auth.ID)
	}
	if response.Auth.Attributes["base_url"] != "https://x.test/v1" {
		t.Fatalf("base_url attribute = %q", response.Auth.Attributes["base_url"])
	}
}

func TestModelProtocolRoutesEachFamily(t *testing.T) {
	cases := map[string]string{
		"deepseek-flash":     formatChat,
		"glm-5.3-flash":      formatChat,
		"gpt-6-luna":         formatResponses,
		"gpt-6.1-sol":        formatResponses,
		"claude-haiku-5-5":   formatMessages,
		"claude-sonnet-5-5":  formatMessages,
		"GPT-6-LUNA":         formatResponses,
		" claude-haiku-5-5 ": formatMessages,
		"some-unknown-model": formatChat,
	}
	for model, want := range cases {
		if got := modelProtocol(model); got != want {
			t.Errorf("modelProtocol(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestUpstreamPathMatchesProtocol(t *testing.T) {
	cases := map[string]string{
		formatChat:      "/chat/completions",
		formatResponses: "/responses",
		formatMessages:  "/messages",
	}
	for protocol, want := range cases {
		if got := upstreamPath(protocol); got != want {
			t.Errorf("upstreamPath(%q) = %q, want %q", protocol, got, want)
		}
	}
}

func TestUpstreamRequestTargetsModelEndpoint(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"deepseek-flash", "https://x.test/v1/chat/completions"},
		{"glm-5.3-flash", "https://x.test/v1/chat/completions"},
		{"gpt-6-luna", "https://x.test/v1/responses"},
		{"claude-haiku-5-5", "https://x.test/v1/messages"},
	}
	for _, tc := range cases {
		req := executorRequest{
			Model:       tc.model,
			Payload:     []byte(`{"model":"` + tc.model + `","messages":[{"role":"user","content":"hi"}]}`),
			StorageJSON: []byte(`{"type":"alysis","api_key":"slk_x","base_url":"https://x.test/v1"}`),
		}
		_, httpReq, err := upstreamRequest(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.model, err)
		}
		if got := httpReq.URL.String(); got != tc.want {
			t.Errorf("%s: url = %q, want %q", tc.model, got, tc.want)
		}
		if got := httpReq.Header.Get("Authorization"); got != "Bearer slk_x" {
			t.Errorf("%s: authorization = %q", tc.model, got)
		}
	}
}

func TestUpstreamPayloadConvertsForNativeProtocols(t *testing.T) {
	chat := []byte(`{"model":"gpt-6-luna","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)

	responses := toUpstreamPayload(executorRequest{Model: "gpt-6-luna", Payload: chat}, formatResponses)
	if !strings.Contains(string(responses), `"input"`) {
		t.Errorf("responses payload lacks an input field: %s", responses)
	}
	if strings.Contains(string(responses), `"messages"`) {
		t.Errorf("responses payload kept chat messages: %s", responses)
	}

	messages := toUpstreamPayload(executorRequest{Model: "claude-haiku-5-5", Payload: chat}, formatMessages)
	if !strings.Contains(string(messages), `"messages"`) {
		t.Errorf("messages payload lacks messages: %s", messages)
	}
	if strings.Contains(string(messages), `"input_text"`) {
		t.Errorf("messages payload kept responses content parts: %s", messages)
	}

	unchanged := toUpstreamPayload(executorRequest{Model: "deepseek-flash", Payload: chat}, formatChat)
	if string(unchanged) != string(chat) {
		t.Errorf("chat payload was rewritten: %s", unchanged)
	}
}

func TestFromUpstreamPayloadRestoresChatShape(t *testing.T) {
	claudeBody := []byte(`{"model":"claude-haiku-5-5","id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	converted := fromUpstreamPayload(executorRequest{Model: "claude-haiku-5-5"}, formatMessages, claudeBody)
	if !strings.Contains(string(converted), `"chat.completion"`) {
		t.Errorf("claude reply was not converted to chat shape: %s", converted)
	}

	responsesBody := []byte(`{"id":"resp_1","object":"response","status":"completed","model":"gpt-6-luna","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}`)
	converted = fromUpstreamPayload(executorRequest{Model: "gpt-6-luna"}, formatResponses, responsesBody)
	if !strings.Contains(string(converted), `"chat.completion"`) {
		t.Errorf("responses reply was not converted to chat shape: %s", converted)
	}
	if !strings.Contains(string(converted), `"OK"`) {
		t.Errorf("responses reply lost its text: %s", converted)
	}

	plain := []byte(`{"id":"x","object":"chat.completion"}`)
	if got := fromUpstreamPayload(executorRequest{Model: "deepseek-flash"}, formatChat, plain); string(got) != string(plain) {
		t.Errorf("chat reply was rewritten: %s", got)
	}
}

func TestWrapResponsesTerminalIsIdempotent(t *testing.T) {
	plain := []byte(`{"id":"resp_1","object":"response","status":"completed"}`)
	wrapped := wrapResponsesTerminal(plain)
	if !strings.Contains(string(wrapped), `"response.completed"`) {
		t.Fatalf("wrapped payload missing terminal type: %s", wrapped)
	}
	if again := wrapResponsesTerminal(wrapped); string(again) != string(wrapped) {
		t.Fatalf("wrapping a terminal event changed it: %s", again)
	}
}

func TestUpstreamErrorCarriesStatusCode(t *testing.T) {
	err := upstreamError(401, []byte(`{"error":{"message":"Invalid or revoked Alysis key."}}`))
	var sc interface{ StatusCode() int }
	if !asStatusCoder(err, &sc) {
		t.Fatal("upstream error does not expose StatusCode")
	}
	if sc.StatusCode() != 401 {
		t.Fatalf("status = %d", sc.StatusCode())
	}
	if !strings.Contains(err.Error(), "Invalid or revoked Alysis key") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestStaticModelsCoverTheHostedCatalog(t *testing.T) {
	want := map[string]bool{
		"deepseek-flash":   false,
		"glm-5.3-flash":    false,
		"gpt-6-luna":       false,
		"claude-haiku-5-5": false,
	}
	for _, model := range staticModels() {
		if _, ok := want[model.ID]; ok {
			want[model.ID] = true
		}
		if model.OwnedBy != providerID {
			t.Errorf("%s owned_by = %q", model.ID, model.OwnedBy)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("static models missing %s", id)
		}
	}
}

func asStatusCoder(err error, target *interface{ StatusCode() int }) bool {
	if coder, ok := err.(interface{ StatusCode() int }); ok {
		*target = coder
		return true
	}
	return false
}
