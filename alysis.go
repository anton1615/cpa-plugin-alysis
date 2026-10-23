package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// providerID is the auth provider key this plugin owns. Auth files declare
// "type": "alysis" and the host routes them here.
const providerID = "alysis"

// Model protocol routing mirrors the official Alysis Code client
// (alysis_code/llm/factory.py resolve_model_protocol): the hosted gateway only
// serves each model on one native endpoint.
const (
	formatChat      = "chat-completions"
	formatResponses = "responses"
	formatMessages  = "anthropic"
)

// modelProtocols pins the models whose upstream endpoint is not chat-completions.
var modelProtocols = map[string]string{
	"gpt-6-luna":        formatResponses,
	"gpt-6.1-sol":       formatResponses,
	"claude-sonnet-5-5": formatMessages,
	"claude-haiku-5-5":  formatMessages,
}

// modelProtocol returns the upstream endpoint family for a model. Models not in
// the table use the OpenAI-compatible chat-completions surface.
func modelProtocol(model string) string {
	if protocol, ok := modelProtocols[strings.ToLower(strings.TrimSpace(model))]; ok {
		return protocol
	}
	return formatChat
}

// upstreamPath maps a protocol family to the path appended to the auth's
// base_url. base_url already includes the gateway's /llm/v1 suffix.
func upstreamPath(protocol string) string {
	switch protocol {
	case formatResponses:
		return "/responses"
	case formatMessages:
		return "/messages"
	default:
		return "/chat/completions"
	}
}

// authStorage is the on-disk shape of an Alysis auth JSON file.
type authStorage struct {
	Type    string `json:"type"`
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Email   string `json:"email,omitempty"`
	Label   string `json:"label,omitempty"`
	// ProxyURL routes the plugin's own gateway calls: "direct" or empty dials
	// the gateway directly, any other value is a http(s)/socks5 proxy URL.
	ProxyURL string `json:"proxy_url,omitempty"`
}

func parseAuthStorage(raw []byte) (authStorage, error) {
	var storage authStorage
	if len(raw) == 0 {
		return storage, errors.New("auth json is empty")
	}
	if err := json.Unmarshal(raw, &storage); err != nil {
		return storage, fmt.Errorf("auth json is not valid JSON: %w", err)
	}
	storage.Type = strings.ToLower(strings.TrimSpace(storage.Type))
	storage.APIKey = strings.TrimSpace(storage.APIKey)
	storage.BaseURL = strings.TrimRight(strings.TrimSpace(storage.BaseURL), "/")
	if storage.Type != providerID {
		return storage, fmt.Errorf("auth json type %q is not %q", storage.Type, providerID)
	}
	if storage.APIKey == "" {
		return storage, errors.New("auth json is missing api_key")
	}
	if storage.BaseURL == "" {
		return storage, errors.New("auth json is missing base_url")
	}
	return storage, nil
}

func (s authStorage) label() string {
	if value := strings.TrimSpace(s.Label); value != "" {
		return value
	}
	if value := strings.TrimSpace(s.Email); value != "" {
		return value
	}
	return providerID
}
