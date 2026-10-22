package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func claudeConverted(thinking string) []byte {
	return []byte(`{"model":"claude-haiku-5-5","max_tokens":64,` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],` +
		`"thinking":` + thinking + `,"stream":false}`)
}

func TestNormalizeClaudeThinkingRewritesEnabledToAdaptive(t *testing.T) {
	converted := claudeConverted(`{"type":"enabled","budget_tokens":24576}`)
	original := []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hi"}],` +
		`"reasoning_effort":"high"}`)

	out := normalizeClaudeThinking(converted, original, "claude-haiku-5-5")
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
	converted := claudeConverted(`{"type":"disabled"}`)
	out := normalizeClaudeThinking(converted, []byte(`{}`), "claude-haiku-5-5")
	if !strings.Contains(string(out), `"disabled"`) {
		t.Fatalf("disabled mode must stay untouched: %s", out)
	}
}

func TestNormalizeClaudeThinkingNoneDisables(t *testing.T) {
	converted := claudeConverted(`{"type":"enabled","budget_tokens":24576}`)
	original := []byte(`{"reasoning_effort":"none"}`)
	out := normalizeClaudeThinking(converted, original, "claude-haiku-5-5")
	if !strings.Contains(string(out), `"disabled"`) {
		t.Fatalf("effort none must disable thinking: %s", out)
	}
}

func TestNormalizeClaudeThinkingDropsUnsupportedEffort(t *testing.T) {
	converted := claudeConverted(`{"type":"enabled","budget_tokens":24576}`)
	original := []byte(`{"reasoning_effort":"bogus"}`)
	out := normalizeClaudeThinking(converted, original, "claude-haiku-5-5")
	if strings.Contains(string(out), "thinking") {
		t.Fatalf("unsupported effort must not send a thinking block: %s", out)
	}
}

func TestNormalizeClaudeThinkingWithoutThinkingBlock(t *testing.T) {
	converted := []byte(`{"model":"claude-haiku-5-5"}`)
	if got := string(normalizeClaudeThinking(converted, []byte(`{}`), "claude-haiku-5-5")); got != string(converted) {
		t.Fatalf("payload changed: %s", got)
	}
}

func TestApplyChatReasoningDropsUnsupportedEffort(t *testing.T) {
	body := []byte(`{"model":"glm-5.3-flash","messages":[],"reasoning_effort":"medium"}`)
	out := applyChatReasoning(body, "glm-5.3-flash")
	if strings.Contains(string(out), "reasoning_effort") {
		t.Fatalf("unsupported effort must be dropped: %s", out)
	}
	kept := applyChatReasoning([]byte(`{"model":"deepseek-flash","reasoning_effort":"high"}`), "deepseek-flash")
	if !strings.Contains(string(kept), `"high"`) {
		t.Fatalf("allowed effort must be kept: %s", kept)
	}
}

func TestApplyChatReasoningKeepsAllowedEffort(t *testing.T) {
	body := []byte(`{"model":"glm-5.3-flash","messages":[],"reasoning_effort":"max"}`)
	out := applyChatReasoning(body, "glm-5.3-flash")
	if !strings.Contains(string(out), `"max"`) {
		t.Fatalf("allowed effort must be kept: %s", out)
	}
}

func TestApplyResponsesReasoningDropsUnsupportedEffort(t *testing.T) {
	converted := []byte(`{"model":"gpt-6-luna","input":"hi","reasoning":{"effort":"bogus"}}`)
	out := applyResponsesReasoning(converted, "gpt-6-luna")
	if strings.Contains(string(out), "reasoning") {
		t.Fatalf("unsupported effort must be dropped: %s", out)
	}
	kept := applyResponsesReasoning([]byte(`{"model":"gpt-6-luna","reasoning":{"effort":"high"}}`), "gpt-6-luna")
	if !strings.Contains(string(kept), `"high"`) {
		t.Fatalf("allowed effort must be kept: %s", kept)
	}
}

func TestRequestEffortReadsBothSpellings(t *testing.T) {
	if got := requestEffort([]byte(`{"reasoning_effort":"high"}`)); got != "high" {
		t.Fatalf("flat effort = %q", got)
	}
	if got := requestEffort([]byte(`{"reasoning":{"effort":"medium"}}`)); got != "medium" {
		t.Fatalf("object effort = %q", got)
	}
	if got := requestEffort([]byte(`{}`)); got != "" {
		t.Fatalf("absent effort = %q", got)
	}
}
