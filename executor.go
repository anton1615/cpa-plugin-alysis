package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	_ "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator/builtin"
)

// hostHTTPRequest mirrors the host.http.do / host.http.do_stream payload.
type hostHTTPRequest struct {
	HostCallbackID string      `json:"host_callback_id,omitempty"`
	Method         string      `json:"method,omitempty"`
	URL            string      `json:"url,omitempty"`
	Headers        http.Header `json:"headers,omitempty"`
	Body           []byte      `json:"body,omitempty"`
}

// hostHTTPResponse mirrors the host.http.do result.
type hostHTTPResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers"`
	Body       []byte      `json:"Body"`
}

// hostHTTPStreamResponse mirrors the host.http.do_stream result. The host
// marshals its own rpcHostHTTPStreamResponse here, so the tags are snake_case
// rather than the Go field names host.http.do uses.
type hostHTTPStreamResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	StreamID   string      `json:"stream_id,omitempty"`
}

// hostHTTPStreamChunk mirrors the host.http.stream_read result.
type hostHTTPStreamChunk struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

// callHost invokes one host callback and decodes its result into out.
func callHost(method string, payload any, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result, err := callHostRaw(method, raw)
	if err != nil {
		return err
	}
	if out == nil || len(result) == 0 {
		return nil
	}
	return json.Unmarshal(result, out)
}

// upstreamRequest builds the HTTP call for one executor request. The payload is
// converted from the client's chat-completions shape into the model's native
// upstream shape, because the hosted gateway rejects chat-completions for the
// Responses and Messages models.
func upstreamRequest(req executorRequest) (hostHTTPRequest, authStorage, error) {
	storage, err := parseAuthStorage(req.StorageJSON)
	if err != nil {
		return hostHTTPRequest{}, storage, err
	}
	protocol := modelProtocol(req.Model)
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("Authorization", "Bearer "+storage.APIKey)
	if req.Stream {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}
	body := toUpstreamPayload(req, protocol)
	return hostHTTPRequest{
		Method:  http.MethodPost,
		URL:     storage.BaseURL + upstreamPath(protocol),
		Headers: headers,
		Body:    body,
	}, storage, nil
}

// toUpstreamPayload converts the host-provided chat-completions payload into the
// model's upstream wire format.
func toUpstreamPayload(req executorRequest, protocol string) []byte {
	body := req.Payload
	if len(body) == 0 {
		body = req.OriginalRequest
	}
	if len(body) == 0 || protocol == formatChat {
		return body
	}
	target := translator.FormatClaude
	if protocol == formatResponses {
		// The built-in registry has no chat -> openai-response transform; the
		// codex transform produces the Responses shape the gateway accepts.
		target = translator.FormatCodex
	}
	if !translator.HasRequestTransformer(translator.FormatOpenAI, target) {
		return body
	}
	return translator.TranslateRequest(translator.FormatOpenAI, target, req.Model, body, req.Stream)
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
	httpReq, _, err := upstreamRequest(req)
	if err != nil {
		return nil, nil, err
	}
	var resp hostHTTPResponse
	if err := callHost("host.http.do", httpReq, &resp); err != nil {
		return nil, nil, fmt.Errorf("upstream request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, upstreamError(resp.StatusCode, resp.Body)
	}
	return fromUpstreamPayload(req, modelProtocol(req.Model), resp.Body), resp.Headers, nil
}

// executeStreamOne performs a streaming upstream call and relays SSE frames as
// chat-completions SSE.
func executeStreamOne(req executorRequest) (http.Header, <-chan []byte, <-chan error, error) {
	httpReq, _, err := upstreamRequest(req)
	if err != nil {
		return nil, nil, nil, err
	}
	httpReq.Headers.Set("Accept", "text/event-stream")
	var stream hostHTTPStreamResponse
	if err := callHost("host.http.do_stream", httpReq, &stream); err != nil {
		return nil, nil, nil, fmt.Errorf("upstream stream failed: %w", err)
	}
	if stream.StatusCode < 200 || stream.StatusCode >= 300 {
		body := drainHostStream(stream.StreamID)
		return nil, nil, nil, upstreamError(stream.StatusCode, body)
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
		defer closeHostStream(stream.StreamID)

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

		for {
			var chunk hostHTTPStreamChunk
			if err := callHost("host.http.stream_read", map[string]string{"stream_id": stream.StreamID}, &chunk); err != nil {
				errs <- err
				return
			}
			if chunk.Error != "" {
				errs <- fmt.Errorf("%s", chunk.Error)
				return
			}
			buffer = append(buffer, chunk.Payload...)
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
			if chunk.Done {
				if len(trimSpaceBytes(buffer)) > 0 {
					emitEvent(append(append([]byte(nil), buffer...), '\n', '\n'))
				}
				return
			}
		}
	}()
	return stream.Headers, payloads, errs, nil
}

func closeHostStream(streamID string) {
	if streamID == "" {
		return
	}
	_ = callHost("host.http.stream_close", map[string]string{"stream_id": streamID}, nil)
}

// drainHostStream collects a failed stream body so the error message carries the
// upstream explanation.
func drainHostStream(streamID string) []byte {
	if streamID == "" {
		return nil
	}
	var collected []byte
	for {
		var chunk hostHTTPStreamChunk
		if err := callHost("host.http.stream_read", map[string]string{"stream_id": streamID}, &chunk); err != nil {
			break
		}
		collected = append(collected, chunk.Payload...)
		if chunk.Error != "" || chunk.Done {
			break
		}
	}
	closeHostStream(streamID)
	return collected
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
