package main

import "strings"

// reasoningContract mirrors the hosted gateway's per-model reasoning contract:
// the effort values a model accepts on its wire. The official client applies
// allowlist-based emission — a knob is only sent when the model documents that
// exact value, otherwise the parameter is left out entirely.
type reasoningContract struct {
	values []string
}

// modelReasoningContracts lists the hosted models and their accepted efforts.
//
//	gpt-6-luna        reasoning:{effort} on /responses
//	deepseek-flash    reasoning_effort on /chat/completions
//	glm-5.3-flash     reasoning_effort on /chat/completions (always on)
//	claude-*-5-5      thinking:{type:adaptive} + output_config.effort
var modelReasoningContracts = map[string]reasoningContract{
	"gpt-6-luna":        {values: []string{"none", "low", "medium", "high", "xhigh", "max"}},
	"deepseek-flash":    {values: []string{"low", "high", "max"}},
	"glm-5.3-flash":     {values: []string{"low", "high", "max"}},
	"claude-haiku-5-5":  {values: []string{"low", "medium", "high", "xhigh", "max"}},
	"claude-sonnet-5-5": {values: []string{"low", "medium", "high", "xhigh", "max"}},
}

func contractFor(model string) (reasoningContract, bool) {
	contract, ok := modelReasoningContracts[strings.ToLower(strings.TrimSpace(model))]
	return contract, ok
}

func (c reasoningContract) allows(effort string) bool {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return false
	}
	for _, value := range c.values {
		if value == effort {
			return true
		}
	}
	return false
}

// effortAllowed reports whether the model accepts the effort. Models without a
// documented contract keep the value untouched.
func effortAllowed(model, effort string) bool {
	contract, ok := contractFor(model)
	if !ok {
		return true
	}
	return contract.allows(effort)
}
