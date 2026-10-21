package main

import (
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// registrationCapability is the capability block the host reads at register time.
type registrationCapability struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
}

// registrationPayload describes this plugin to the host.
type registrationPayload struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

func registration(request []byte) (registrationPayload, error) {
	cloudAPIBase = parsePluginConfig(request).CloudAPIURL
	return registrationPayload{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             providerID,
			Version:          "0.1.1",
			Author:           "anton1615",
			GitHubRepository: "https://github.com/anton1615/cpa-plugin-alysis",
			ConfigFields:     []pluginapi.ConfigField{},
		},
		Capabilities: registrationCapability{
			ModelProvider:      true,
			AuthProvider:       true,
			Executor:           true,
			ExecutorModelScope: pluginapi.ExecutorModelScopeOAuth,
			// Declaring all three lets the host hand requests over untranslated
			// when the client already speaks the model's native protocol.
			ExecutorInputFormats:  []string{formatChat, formatResponses, formatMessages},
			ExecutorOutputFormats: []string{formatChat, formatResponses, formatMessages},
		},
	}, nil
}

// staticModels is the catalog the hosted gateway serves today.
func staticModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{
		modelInfo("deepseek-flash", "DeepSeek V4.1 Flash"),
		modelInfo("glm-5.3-flash", "GLM 5.3 Flash"),
		modelInfo("gpt-6-luna", "GPT-6 Luna"),
		modelInfo("claude-haiku-5-5", "Claude Haiku 5.5"),
		modelInfo("claude-sonnet-5-5", "Claude Sonnet 5.5"),
	}
}

func modelInfo(id, display string) pluginapi.ModelInfo {
	return pluginapi.ModelInfo{
		ID:          id,
		Object:      "model",
		OwnedBy:     providerID,
		Type:        "model",
		DisplayName: display,
		Name:        id,
	}
}
