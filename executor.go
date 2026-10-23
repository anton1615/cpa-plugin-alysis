package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	_ "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator/builtin"
)

// upstreamRequest builds the HTTP call for one executor request. The payload is
// converted from the client's chat-completions shape into the model's native
// upstream shape, because the hosted gateway rejects chat-completions for the
// Responses and Messages models.
func upstreamRequest(req executorRequest) (authStorage, *http.Request, error) {
	storage, err := parseAuthStorage(req.StorageJSON)
	if err != nil {
		return storage, nil, err
	}
	protocol := modelProtocol(req.Model)
	body := toUpstreamPayload(req, protocol)
	httpReq, err := http.NewRequest(
		http.MethodPost,
		storage.BaseURL+upstreamPath(protocol),
		bytes.NewReader(body),
	)
	if err != nil {
		return storage, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+storage.APIKey)
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	return storage, httpReq, nil
}

// toUpstreamPayload converts the host-provided chat-completions payload into the
// model's upstream wire format.
func toUpstreamPayload(req executorRequest, protocol string) []byte {
	body := req.Payload
	if len(body) == 0 {
		body = req.OriginalRequest
	}
	if len(body) == 0 {
		return body
	}
	switch protocol {
	case formatResponses:
		if !translator.HasRequestTransformer(translator.FormatOpenAI, translator.FormatCodex) {
			return body
		}
		converted := translator.TranslateRequest(translator.FormatOpenAI, translator.FormatCodex, req.Model, body, req.Stream)
		return applyResponsesReasoning(converted, req.Model)
	case formatMessages:
		if !translator.HasRequestTransformer(translator.FormatOpenAI, translator.FormatClaude) {
			return body
		}
		converted := translator.TranslateRequest(translator.FormatOpenAI, translator.FormatClaude, req.Model, body, req.Stream)
		return normalizeClaudeThinking(converted, body, req.Model)
	default:
		return applyChatReasoning(body, req.Model)
	}
}

// requestEffort reads the reasoning effort a client asked for. The host
// translates client protocols to chat, where the knob is a flat string.
func requestEffort(body []byte) string {
	var payload struct {
		ReasoningEffort string `json:"reasoning_effort"`
		Reasoning       struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if effort := strings.ToLower(strings.TrimSpace(payload.ReasoningEffort)); effort != "" {
		return effort
	}
	return strings.ToLower(strings.TrimSpace(payload.Reasoning.Effort))
}

// applyChatReasoning drops a flat reasoning_effort the model does not accept.
// The gateway rejects unknown values (GLM answers "reasoning_effort must be
// low, high, or max"), and the official client omits them the same way.
func applyChatReasoning(body []byte, model string) []byte {
	effort := requestEffort(body)
	if effort == "" || effortAllowed(model, effort) {
		return body
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		return body
	}
	delete(document, "reasoning_effort")
	delete(document, "reasoning")
	filtered, err := json.Marshal(document)
	if err != nil {
		return body
	}
	return filtered
}

// applyResponsesReasoning drops a reasoning object whose effort the model does
// not accept ("Invalid Luna effort." on the hosted gateway).
func applyResponsesReasoning(converted []byte, model string) []byte {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(converted, &document); err != nil {
		return converted
	}
	raw, ok := document["reasoning"]
	if !ok {
		return converted
	}
	var reasoning struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(raw, &reasoning); err != nil {
		return converted
	}
	if reasoning.Effort == "" || effortAllowed(model, reasoning.Effort) {
		return converted
	}
	delete(document, "reasoning")
	filtered, err := json.Marshal(document)
	if err != nil {
		return converted
	}
	return filtered
}

// normalizeClaudeThinking rewrites the Claude thinking block into the mode the
// hosted gateway accepts. The gateway rejects the legacy "enabled" mode and
// only understands "adaptive"; the requested effort moves to output_config.
func normalizeClaudeThinking(converted, original []byte, model string) []byte {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(converted, &document); err != nil {
		return converted
	}
	effort := requestEffort(original)
	allowed := effort == "" || effortAllowed(model, effort)

	raw, hasThinking := document["thinking"]
	thinking := map[string]any{}
	if hasThinking {
		if err := json.Unmarshal(raw, &thinking); err != nil {
			return converted
		}
	}
	if effort == "none" {
		// Explicit disable is the documented off switch for these models.
		thinking["type"] = "disabled"
		delete(thinking, "budget_tokens")
		document["thinking"], _ = json.Marshal(thinking)
		delete(document, "output_config")
		out, err := json.Marshal(document)
		if err != nil {
			return converted
		}
		return out
	}
	if !allowed {
		// The model does not accept this effort: send nothing.
		delete(document, "thinking")
		delete(document, "output_config")
		out, err := json.Marshal(document)
		if err != nil {
			return converted
		}
		return out
	}
	if fmt.Sprint(thinking["type"]) == "enabled" {
		delete(thinking, "budget_tokens")
		thinking["type"] = "adaptive"
		document["thinking"], _ = json.Marshal(thinking)
	}
	if effort != "" {
		var config map[string]any
		if rawConfig, ok := document["output_config"]; ok {
			_ = json.Unmarshal(rawConfig, &config)
		}
		if config == nil {
			config = map[string]any{}
		}
		config["effort"] = effort
		document["output_config"], _ = json.Marshal(config)
	}
	out, err := json.Marshal(document)
	if err != nil {
		return converted
	}
	return out
}

// fromUpstreamPayload converts an upstream non-streaming body into the
// chat-completions shape the host expects from this executor.
func fromUpstreamPayload(req executorRequest, protocol string, body []byte) []byte {
	if len(body) == 0 || protocol == formatChat {
		return body
	}
	source := translator.FormatClaude
	if protocol == formatResponses {
		source = translator.FormatCodex
		// The codex response converter keys on a terminal Responses event.
		body = wrapResponsesTerminal(body)
	}
	converted := translator.TranslateNonStream(
		nil, source, translator.FormatOpenAI, req.Model,
		req.Payload, req.Payload, body, nil,
	)
	if len(converted) == 0 {
		return body
	}
	return converted
}

// wrapResponsesTerminal wraps a completed Responses object as the terminal
// stream event the codex response converter understands.
func wrapResponsesTerminal(body []byte) []byte {
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		return body
	}
	if _, ok := object["type"]; ok {
		return body
	}
	wrapped, err := json.Marshal(map[string]any{
		"type":     "response.completed",
		"response": object,
	})
	if err != nil {
		return body
	}
	return wrapped
}

// executeOne performs a non-streaming upstream call and converts the reply back
// to chat-completions.
func executeOne(req executorRequest) ([]byte, http.Header, error) {
	storage, httpReq, err := upstreamRequest(req)
	if err != nil {
		return nil, nil, err
	}
	client, errClient := upstreamClient(storage.ProxyURL)
	if errClient != nil {
		return nil, nil, errClient
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	resp, errDo := client.Do(httpReq.WithContext(ctx))
	if errDo != nil {
		return nil, nil, fmt.Errorf("upstream request failed: %w", errDo)
	}
	defer resp.Body.Close()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, nil, fmt.Errorf("upstream body read failed: %w", errRead)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, upstreamError(resp.StatusCode, body)
	}
	return fromUpstreamPayload(req, modelProtocol(req.Model), body), resp.Header, nil
}

// executeStreamOne performs a streaming upstream call and relays SSE frames as
// chat-completions SSE. Unlike executeOne there is no total timeout: reasoning
// streams idle for minutes while upstream chunks keep flowing.
func executeStreamOne(req executorRequest) (http.Header, <-chan []byte, <-chan error, error) {
	storage, httpReq, err := upstreamRequest(req)
	if err != nil {
		return nil, nil, nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	client, errClient := upstreamClient(storage.ProxyURL)
	if errClient != nil {
		return nil, nil, nil, errClient
	}
	resp, errDo := client.Do(httpReq)
	if errDo != nil {
		return nil, nil, nil, fmt.Errorf("upstream stream failed: %w", errDo)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, nil, nil, upstreamError(resp.StatusCode, body)
	}

	protocol := modelProtocol(req.Model)
	source := translator.FormatClaude
	if protocol == formatResponses {
		source = translator.FormatCodex
	}
	translate := protocol != formatChat

	payloads := make(chan []byte)
	errs := make(chan error, 1)
	go func() {
		defer close(payloads)
		defer close(errs)
		defer resp.Body.Close()

		var buffer []byte
		var param any
		// The host frames every emitted chunk as "data: <payload>\n\n", so
		// the payloads must be bare JSON objects or the string "[DONE]".
		emitEvent := func(event []byte) bool {
			data := extractSSEData(event)
			if len(data) == 0 {
				return true
			}
			if string(data) == doneMarker {
				payloads <- []byte(doneMarker)
				return false
			}
			if !translate {
				payloads <- data
				return true
			}
			for _, frame := range translator.TranslateStream(
				nil, source, translator.FormatOpenAI, req.Model,
				req.Payload, req.Payload, append(dataTag, data...), &param,
			) {
				payloads <- frame
			}
			return true
		}

		chunk := make([]byte, 16*1024)
		for {
			n, errRead := resp.Body.Read(chunk)
			if n > 0 {
				buffer = append(buffer, chunk[:n]...)
				for {
					index := bytes.Index(buffer, eventBreak)
					if index < 0 {
						break
					}
					event := buffer[:index+len(eventBreak)]
					buffer = buffer[index+len(eventBreak):]
					if !emitEvent(event) {
						return
					}
				}
			}
			if errRead != nil {
				if errRead == io.EOF {
					if len(trimSpaceBytes(buffer)) > 0 {
						emitEvent(append(append([]byte(nil), buffer...), '\n', '\n'))
					}
					return
				}
				errs <- fmt.Errorf("upstream stream read failed: %w", errRead)
				return
			}
		}
	}()
	return resp.Header, payloads, errs, nil
}

// eventBreak terminates one SSE event.
var eventBreak = []byte("\n\n")

// doneMarker terminates an OpenAI chat-completions SSE stream.
const doneMarker = "[DONE]"

// dataTag prefixes the data payload of one SSE event.
var dataTag = []byte("data:")

// extractSSEData joins the data lines of one complete SSE event, dropping
// comment, event, and id lines the upstream interleaves.
func extractSSEData(event []byte) []byte {
	var lines []byte
	rest := event
	for len(rest) > 0 {
		index := bytes.IndexByte(rest, '\n')
		var line []byte
		if index < 0 {
			line, rest = rest, nil
		} else {
			line, rest = rest[:index], rest[index+1:]
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		if bytes.HasPrefix(line, dataTag) {
			payload := bytes.TrimSpace(line[len(dataTag):])
			lines = append(lines, payload...)
			lines = append(lines, '\n')
		}
	}
	return bytes.TrimSuffix(lines, []byte("\n"))
}

func trimSpaceBytes(raw []byte) []byte { return bytes.TrimSpace(raw) }

// upstreamError mirrors an upstream HTTP failure so the host surfaces the real
// status (401 invalid_api_key, 429 quota, ...) and can rotate to another auth.
func upstreamError(status int, body []byte) error {
	return &statusError{status: status, message: upstreamMessage(status, body)}
}

func upstreamMessage(status int, body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Sprintf("upstream returned HTTP %d", status)
	}
	const limit = 512
	if len(text) > limit {
		text = text[:limit] + "…"
	}
	return text
}

type statusError struct {
	status  int
	message string
}

func (e *statusError) Error() string { return e.message }

// StatusCode lets the host classify the failure (401 rotates the credential
// instead of disabling the auth file).
func (e *statusError) StatusCode() int { return e.status }

var _ pluginapi.ExecutorResponse
