package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// fetchGatewayModels performs GET <base_url>/models with the account key.
// Indirected so tests can replace the transport.
var fetchGatewayModels = defaultFetchGatewayModels

func defaultFetchGatewayModels(baseURL, apiKey string) (int, []byte, error) {
	request := hostHTTPRequest{
		Method:  "GET",
		URL:     baseURL + "/models",
		Headers: http.Header{"Authorization": []string{"Bearer " + apiKey}},
	}
	var response hostHTTPResponse
	if err := callHost("host.http.do", request, &response); err != nil {
		return 0, nil, err
	}
	return response.StatusCode, response.Body, nil
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
	status, body, err := fetchGatewayModels(storage.BaseURL, storage.APIKey)
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
