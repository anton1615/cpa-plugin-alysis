package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeClaudeThinkingRewritesEnabledToAdaptive(t *testing.T) {
	converted := []byte(`{"model":"claude-haiku-5-5","max_tokens":64,` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],` +
		`"thinking":{"type":"enabled","budget_tokens":24576},"stream":false}`)
	original := []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hi"}],` +
		`"reasoning_effort":"high"}`)

	out := normalizeClaudeThinking(converted, original)
	var document map[string]any
	if err := json.Unmarshal(out, &document); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	thinking, _ := document["thinking"].(map[string]any)
	if thinking == nil {
		t.Fatal("thinking block missing")
	}
	if thinking["type"] != "adaptive" {
		t.Fatalf("thinking type = %v", thinking["type"])
	}
	if _, ok := thinking["budget_tokens"]; ok {
		t.Fatal("budget_tokens must be dropped for the adaptive mode")
	}
	config, _ := document["output_config"].(map[string]any)
	if config == nil || config["effort"] != "high" {
		t.Fatalf("output_config = %v", document["output_config"])
	}
}

func TestNormalizeClaudeThinkingKeepsOtherModes(t *testing.T) {
	converted := []byte(`{"thinking":{"type":"disabled"}}`)
	out := normalizeClaudeThinking(converted, []byte(`{}`))
	if !strings.Contains(string(out), `"disabled"`) {
		t.Fatalf("disabled mode must stay untouched: %s", out)
	}
}

func TestNormalizeClaudeThinkingWithoutThinkingBlock(t *testing.T) {
	converted := []byte(`{"model":"claude-haiku-5-5"}`)
	if got := string(normalizeClaudeThinking(converted, []byte(`{}`))); got != string(converted) {
		t.Fatalf("payload changed: %s", got)
	}
}

func TestReasoningEffortAcceptsKnownLevels(t *testing.T) {
	cases := map[string]string{
		`{"reasoning_effort":"high"}`:    "high",
		`{"reasoning_effort":"MEDIUM"}`:  "medium",
		`{"reasoning_effort":"low"}`:     "low",
		`{"reasoning_effort":"minimal"}`: "",
		`{}`:                             "",
	}
	for body, want := range cases {
		if got := reasoningEffort([]byte(body)); got != want {
			t.Fatalf("reasoningEffort(%s) = %q, want %q", body, got, want)
		}
	}
}
