package main

import (
	"encoding/json"
	"testing"
)

func TestModelsForAuthUsesAccountCatalog(t *testing.T) {
	previous := fetchGatewayModels
	fetchGatewayModels = func(baseURL, apiKey, proxyURL string) (int, []byte, error) {
		if baseURL != "https://example.test/llm/v1" {
			t.Errorf("base url = %q", baseURL)
		}
		if apiKey != "slk_key" {
			t.Errorf("api key = %q", apiKey)
		}
		return 200, []byte(`{"object":"list","data":[{"id":"deepseek-flash"},{"id":"claude-sonnet-5-5"}]}`), nil
	}
	defer func() { fetchGatewayModels = previous }()

	storage, _ := json.Marshal(map[string]string{
		"type": "alysis", "api_key": "slk_key", "base_url": "https://example.test/llm/v1",
	})
	request, _ := json.Marshal(map[string]any{"StorageJSON": storage})

	response := modelsForAuth(request)
	if response.Provider != providerID {
		t.Fatalf("provider = %q", response.Provider)
	}
	if len(response.Models) != 2 {
		t.Fatalf("models = %+v", response.Models)
	}
	if response.Models[0].ID != "deepseek-flash" || response.Models[1].ID != "claude-sonnet-5-5" {
		t.Fatalf("model ids = %+v", response.Models)
	}
	if response.Models[1].DisplayName != "Claude Sonnet 5.5" {
		t.Fatalf("display name = %q", response.Models[1].DisplayName)
	}
}

func TestModelsForAuthFallsBackToStaticCatalog(t *testing.T) {
	previous := fetchGatewayModels
	fetchGatewayModels = func(string, string, string) (int, []byte, error) {
		return 403, []byte(`{"error":"not allowed"}`), nil
	}
	defer func() { fetchGatewayModels = previous }()

	storage, _ := json.Marshal(map[string]string{
		"type": "alysis", "api_key": "slk_key", "base_url": "https://example.test/llm/v1",
	})
	request, _ := json.Marshal(map[string]any{"StorageJSON": storage})

	response := modelsForAuth(request)
	if len(response.Models) != len(staticModels()) {
		t.Fatalf("fallback models = %+v", response.Models)
	}
	for _, model := range response.Models {
		if model.ID == "claude-sonnet-5-5" {
			t.Fatal("fallback catalog must not list a plan-gated model")
		}
	}
}

func TestModelsForAuthRejectsForeignAuth(t *testing.T) {
	response := modelsForAuth([]byte(`{"StorageJSON":"bm90LWpzb24="}`))
	if len(response.Models) != len(staticModels()) {
		t.Fatalf("models = %+v", response.Models)
	}
}
