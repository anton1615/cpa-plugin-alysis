package main

import (
	"encoding/json"
	"testing"
)

type testMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type testPart struct {
	Type     string `json:"type"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

func imageMessage(role, url string, extra string) string {
	return `{"role":"` + role + `","content":[` + extra +
		`{"type":"image_url","image_url":{"url":"` + url + `","detail":"auto"}}]}`
}

func messagesOf(t *testing.T, body []byte) []testMessage {
	t.Helper()
	var document struct {
		Messages []testMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return document.Messages
}

// partsOf decodes one message's content parts; plain string content has none.
func partsOf(t *testing.T, message testMessage) []testPart {
	t.Helper()
	var parts []testPart
	if err := json.Unmarshal(message.Content, &parts); err != nil {
		return nil
	}
	return parts
}

func imageURLs(t *testing.T, message testMessage) []string {
	t.Helper()
	var urls []string
	for _, part := range partsOf(t, message) {
		if part.Type == "image_url" {
			urls = append(urls, part.ImageURL.URL)
		}
	}
	return urls
}

// A tool-result image whose bytes already reached the model in a user message
// is dropped, because the gateway rejects an image outside a user message and
// a second copy would only duplicate the base64.
func TestNormalizeChatImagesDropsDuplicateToolImage(t *testing.T) {
	url := "data:image/webp;base64,UklGRg=="
	body := []byte(`{"model":"deepseek-flash","messages":[` +
		imageMessage("user", url, `{"type":"text","text":"what is this"},`) + `,` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function",` +
		`"function":{"name":"read","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"c1","content":[{"type":"text","text":"read it"},` +
		`{"type":"image_url","image_url":{"url":"` + url + `","detail":"auto"}}]}]}`)

	out := normalizeChatImages(body)
	messages := messagesOf(t, out)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want the original 3 (nothing hoisted)", len(messages))
	}
	if got := imageURLs(t, messages[0]); len(got) != 1 || got[0] != url {
		t.Fatalf("user image = %v, want it untouched", got)
	}
	if got := imageURLs(t, messages[2]); len(got) != 0 {
		t.Fatalf("tool image = %v, want the image dropped", got)
	}
	var tool struct {
		ToolCallID string     `json:"tool_call_id"`
		Content    []testPart `json:"content"`
	}
	var raw struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := json.Unmarshal(raw.Messages[2], &tool); err != nil {
		t.Fatalf("unmarshal tool message: %v", err)
	}
	if tool.ToolCallID != "c1" || len(tool.Content) != 1 || tool.Content[0].Type != "text" {
		t.Fatalf("tool message = %+v, want its id and only the text part", tool)
	}
}

// A tool-result image the model has not seen yet is carried into a trailing
// user message, the shape the official CLI uses for tool-result pixels.
func TestNormalizeChatImagesHoistsUniqueToolImage(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","messages":[` +
		`{"role":"user","content":"show the screenshot"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function",` +
		`"function":{"name":"capture","arguments":"{}"}}]},` +
		imageMessage("tool", "data:image/png;base64,AAAA", `{"type":"text","text":"captured"},`) + `]}`)

	out := normalizeChatImages(body)
	messages := messagesOf(t, out)
	if len(messages) != 4 {
		t.Fatalf("messages = %d, want a fourth user message carrying the image", len(messages))
	}
	if got := imageURLs(t, messages[2]); len(got) != 0 {
		t.Fatalf("tool image = %v, want the image moved out", got)
	}
	if messages[3].Role != "user" {
		t.Fatalf("trailing role = %q, want user", messages[3].Role)
	}
	if got := imageURLs(t, messages[3]); len(got) != 1 || got[0] != "data:image/png;base64,AAAA" {
		t.Fatalf("hoisted image = %v, want the tool-result image", got)
	}
}

// A tool message left with no parts gets "" content rather than a null, which
// the gateway rejects.
func TestNormalizeChatImagesEmptiesImageOnlyToolMessage(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","messages":[` +
		`{"role":"user","content":"hi"},` +
		imageMessage("tool", "data:image/png;base64,AAAA", "") + `,` +
		`{"role":"user","content":"continue"}]}`)

	out := normalizeChatImages(body)
	messages := messagesOf(t, out)
	if string(messages[1].Content) != `""` {
		t.Fatalf("tool content = %s, want an empty string", messages[1].Content)
	}
	if got := imageURLs(t, messages[3]); len(got) != 1 {
		t.Fatalf("hoisted image = %v, want the tool-result image", got)
	}
}

// Text-only payloads must come back byte-identical: the rewrite runs on every
// chat request.
func TestNormalizeChatImagesLeavesTextOnlyPayloadAlone(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","messages":[` +
		`{"role":"user","content":[{"type":"text","text":"hi"}]},` +
		`{"role":"assistant","content":"hello"}]}`)
	if out := normalizeChatImages(body); string(out) != string(body) {
		t.Fatalf("payload was rewritten: %s", out)
	}
}
