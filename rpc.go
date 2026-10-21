package main

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// executorRequest mirrors the wire fields of pluginapi.ExecutorRequest. The
// original type carries an interface field (HTTPClient) that cannot cross the
// C ABI, so the JSON shape is re-declared here.
type executorRequest struct {
	AuthID          string            `json:"AuthID"`
	AuthProvider    string            `json:"AuthProvider"`
	Model           string            `json:"Model"`
	Format          string            `json:"Format"`
	Stream          bool              `json:"Stream"`
	Alt             string            `json:"Alt"`
	Headers         http.Header       `json:"Headers"`
	OriginalRequest []byte            `json:"OriginalRequest"`
	SourceFormat    string            `json:"SourceFormat"`
	Payload         []byte            `json:"Payload"`
	StorageJSON     []byte            `json:"StorageJSON"`
	AuthAttributes  map[string]string `json:"AuthAttributes"`
}

func decodeExecutorRequest(raw []byte) (executorRequest, error) {
	var req executorRequest
	if len(raw) == 0 {
		return req, nil
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, err
	}
	return req, nil
}

// executeExecutor runs one non-streaming upstream call.
func executeExecutor(raw []byte) (pluginapi.ExecutorResponse, error) {
	req, err := decodeExecutorRequest(raw)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	body, headers, err := executeOne(req)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	return pluginapi.ExecutorResponse{Payload: body, Headers: headers}, nil
}

// executeExecutorStream runs one streaming upstream call. The host reads the
// stream through host.http.stream_read, so this returns after opening it.
func executeExecutorStream(raw []byte) (streamResponse, error) {
	req, err := decodeExecutorRequest(raw)
	if err != nil {
		return streamResponse{}, err
	}
	headers, payloads, errs, err := executeStreamOne(req)
	if err != nil {
		return streamResponse{}, err
	}
	chunks := make([]pluginapi.ExecutorStreamChunk, 0, 16)
	for {
		select {
		case payload, ok := <-payloads:
			if !ok {
				if streamErr, okErr := <-errs; okErr && streamErr != nil {
					chunks = append(chunks, pluginapi.ExecutorStreamChunk{Err: streamErr})
				}
				return streamResponse{Headers: headers, Chunks: chunks}, nil
			}
			chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: payload})
		}
	}
}

// streamResponse is the wire shape the host decodes for executor.execute_stream.
type streamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}
