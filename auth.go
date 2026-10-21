package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// authParseRequest is the host payload for auth.parse.
type authParseRequest struct {
	Provider string          `json:"Provider"`
	Path     string          `json:"Path"`
	FileName string          `json:"FileName"`
	RawJSON  json.RawMessage `json:"RawJSON"`
}

// parseAuth turns one auth JSON file into a host auth record.
func parseAuth(request []byte) pluginapi.AuthParseResponse {
	var req authParseRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	storage, err := parseAuthStorage(req.RawJSON)
	if err != nil {
		// Not our file: report unhandled so the host keeps trying other parsers.
		return pluginapi.AuthParseResponse{Handled: false}
	}
	return pluginapi.AuthParseResponse{
		Handled: true,
		Auth: pluginapi.AuthData{
			Provider:    providerID,
			ID:          authID(req.FileName, storage),
			FileName:    req.FileName,
			Label:       storage.label(),
			StorageJSON: req.RawJSON,
			Metadata:    map[string]any{"type": providerID, "email": storage.Email},
			Attributes:  map[string]string{"base_url": storage.BaseURL},
		},
	}
}

// authID keeps the host auth identifier stable and unique per credential.
func authID(fileName string, storage authStorage) string {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(fileName)), ".json")
	if name != "" {
		return providerID + ":" + name
	}
	suffix := storage.APIKey
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return providerID + ":" + suffix
}

// authRefreshRequest is the host payload for auth.refresh.
type authRefreshRequest struct {
	StorageJSON json.RawMessage `json:"StorageJSON"`
}

// refreshAuth re-emits the stored credential unchanged: Alysis gateway keys have
// no expiry and no refresh token, so there is nothing to rotate.
func refreshAuth(request []byte) pluginapi.AuthRefreshResponse {
	var req authRefreshRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	storage, err := parseAuthStorage(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}
	}
	return pluginapi.AuthRefreshResponse{
		Auth: pluginapi.AuthData{
			Provider:    providerID,
			Label:       storage.label(),
			StorageJSON: req.StorageJSON,
			Metadata:    map[string]any{"type": providerID, "email": storage.Email},
			Attributes:  map[string]string{"base_url": storage.BaseURL},
		},
	}
}

var _ = fmt.Sprintf
