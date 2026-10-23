package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUpstreamClientHonorsProxyURL(t *testing.T) {
	for _, setting := range []string{"", "direct"} {
		client, err := upstreamClient(setting)
		if err != nil {
			t.Fatalf("%q: %v", setting, err)
		}
		transport := client.Transport.(*http.Transport)
		if transport.Proxy != nil {
			t.Fatalf("%q: direct mode must not set a proxy", setting)
		}
	}
	client, err := upstreamClient("http://proxy.test:8080")
	if err != nil {
		t.Fatalf("http proxy: %v", err)
	}
	if client.Transport.(*http.Transport).Proxy == nil {
		t.Fatal("http proxy mode must set a proxy")
	}
	if _, err := upstreamClient("socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("socks5 proxy: %v", err)
	}
	if _, err := upstreamClient("gopher://x"); err == nil {
		t.Fatal("unsupported scheme must be rejected")
	}
}

func storageFor(serverURL string) executorRequest {
	return executorRequest{
		Model:       "deepseek-flash",
		Payload:     []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`),
		StorageJSON: []byte(`{"type":"alysis","api_key":"slk_x","base_url":"` + serverURL + `"}`),
	}
}

func TestExecuteOneDialsDirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer slk_x" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer server.Close()

	body, _, err := executeOne(storageFor(server.URL))
	if err != nil {
		t.Fatalf("executeOne: %v", err)
	}
	if !contains(body, []byte(`"OK"`)) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestExecuteOneSurfacedAsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid_api_key"}}`))
	}))
	defer server.Close()

	_, _, err := executeOne(storageFor(server.URL))
	statusErr, ok := err.(interface{ StatusCode() int })
	if !ok {
		t.Fatalf("error must carry a status code, got %v", err)
	}
	if statusErr.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("status = %d", statusErr.StatusCode())
	}
}

func TestExecuteStreamOneRelaysSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"a\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"O\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"a\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"K\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	req := storageFor(server.URL)
	req.Stream = true
	_, payloads, errs, err := executeStreamOne(req)
	if err != nil {
		t.Fatalf("executeStreamOne: %v", err)
	}
	joined := []byte{}
	deadline := time.After(10 * time.Second)
	for frames := 0; frames < 3; {
		select {
		case frame, ok := <-payloads:
			if !ok {
				goto done
			}
			if !contains(frame, []byte(`"O"`)) && !contains(frame, []byte(`"K"`)) && string(frame) != doneMarker {
				t.Fatalf("unexpected frame: %s", frame)
			}
			joined = append(joined, frame...)
			frames++
		case err := <-errs:
			t.Fatalf("stream error: %v", err)
		case <-deadline:
			t.Fatal("stream did not finish")
		}
	}
done:
	if !contains(joined, []byte(`"O"`)) || !contains(joined, []byte(`"K"`)) {
		t.Fatalf("missing frames: %s", joined)
	}
}

func contains(haystack, needle []byte) bool {
	return bytes.Contains(haystack, needle)
}
