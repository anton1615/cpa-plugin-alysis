package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// fetchGatewayModels performs GET <base_url>/models with the account key.
// Indirected so tests can replace the transport.
var fetchGatewayModels = defaultFetchGatewayModels

func defaultFetchGatewayModels(baseURL, apiKey, proxyURL string) (int, []byte, error) {
	client, errClient := upstreamClient(proxyURL)
	if errClient != nil {
		return 0, nil, errClient
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if errRequest != nil {
		return 0, nil, errRequest
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	resp, errDo := client.Do(request)
	if errDo != nil {
		return 0, nil, errDo
	}
	defer resp.Body.Close()
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return 0, nil, errRead
	}
	return resp.StatusCode, body, nil
}

// modelsForAuth publishes the models the account's hosted plan really serves.
// The hosted catalog is plan-dependent, so the static list is only a fallback
// for when the discovery call fails.
func modelsForAuth(request []byte) pluginapi.ModelResponse {
	fallback := pluginapi.ModelResponse{Provider: providerID, Models: staticModels()}
	var payload struct {
		StorageJSON []byte `json:"StorageJSON"`
	}
	if len(request) > 0 {
		_ = json.Unmarshal(request, &payload)
	}
	storage, err := parseAuthStorage(payload.StorageJSON)
	if err != nil {
		return fallback
	}
	status, body, err := fetchGatewayModels(storage.BaseURL, storage.APIKey, storage.ProxyURL)
	if err != nil || status < 200 || status >= 300 {
		return fallback
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return fallback
	}
	models := make([]pluginapi.ModelInfo, 0, len(catalog.Data))
	for _, entry := range catalog.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		models = append(models, modelInfo(id, displayName(id)))
	}
	if len(models) == 0 {
		return fallback
	}
	return pluginapi.ModelResponse{Provider: providerID, Models: models}
}

// displayName returns a friendly label for a known model id.
func displayName(id string) string {
	if name, ok := modelDisplayNames[id]; ok {
		return name
	}
	return id
}
