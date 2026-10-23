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
			Version:          "0.1.7",
			Author:           "anton1615",
			GitHubRepository: "https://github.com/anton1615/cpa-plugin-alysis",
			ConfigFields:     []pluginapi.ConfigField{},
		},
		Capabilities: registrationCapability{
			ModelProvider:      true,
			AuthProvider:       true,
			Executor:           true,
			ExecutorModelScope: pluginapi.ExecutorModelScopeOAuth,
			// Chat only: the host translates the client protocol (Responses or
			// Messages) to chat and back. The plugin converts chat to the
			// per-model upstream format itself, so accepting the client
			// formats here would hand it payloads it cannot parse.
			ExecutorInputFormats:  []string{formatChat},
			ExecutorOutputFormats: []string{formatChat},
		},
	}, nil
}

// staticModels is the fallback catalog used when per-auth discovery fails.
// Models that the hosted plan gates behind a higher tier are not listed here;
// they appear once the account catalog reports them.
func staticModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{
		modelInfo("deepseek-flash", modelDisplayNames["deepseek-flash"]),
		modelInfo("glm-5.3-flash", modelDisplayNames["glm-5.3-flash"]),
		modelInfo("gpt-6-luna", modelDisplayNames["gpt-6-luna"]),
		modelInfo("claude-haiku-5-5", modelDisplayNames["claude-haiku-5-5"]),
	}
}

// modelDisplayNames maps model ids to human readable labels.
var modelDisplayNames = map[string]string{
	"deepseek-flash":    "DeepSeek V4.1 Flash",
	"glm-5.3-flash":     "GLM 5.3 Flash",
	"gpt-6-luna":        "GPT-6 Luna",
	"claude-sonnet-5-5": "Claude Sonnet 5.5",
	"claude-haiku-5-5":  "Claude Haiku 5.5",
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
