package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

// Exercise the actual executor, including ApplyThinking and payload sanitation,
// against a loopback upstream. A translator-only pass must not hide a later
// override that restores xhigh on the wire.
func TestCodexExecutorClaudeOutputConfigEffort(t *testing.T) {
	cases := []struct {
		name   string
		fields string
		want   string
	}{
		{"low", `"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}`, "low"},
		{"medium", `"thinking":{"type":"adaptive","display":"updates"},"output_config":{"effort":"medium"}`, "medium"},
		{"high", `"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}`, "high"},
		{"xhigh", `"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"}`, "xhigh"},
		{"max", `"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}`, "max"},
		{"missing", `"thinking":{"type":"adaptive"}`, "xhigh"},
		{"invalid", `"thinking":{"type":"adaptive"},"output_config":{"effort":"ultra"}`, "xhigh"},
		{"disabled", `"thinking":{"type":"disabled"},"output_config":{"effort":"high"}`, "none"},
	}
	for _, tc := range cases {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, streaming), func(t *testing.T) {
				requests := make(chan []byte, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/backend-api/codex/responses" {
						t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
						http.Error(w, "unexpected route", http.StatusNotFound)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read upstream body: %v", err)
						http.Error(w, "read failed", http.StatusBadRequest)
						return
					}
					requests <- body
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_effort\",\"object\":\"response\",\"created_at\":1710000002,\"status\":\"in_progress\",\"model\":\"gpt-6.1-sol\"}}\n\n"+
						"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg_effort\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n"+
						"data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"PONG\"}\n\n"+
						"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_effort\",\"object\":\"response\",\"created_at\":1710000002,\"status\":\"completed\",\"model\":\"gpt-6.1-sol\",\"output\":[{\"id\":\"msg_effort\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"PONG\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"total_tokens\":12}}}\n\n")
				}))
				defer server.Close()

				executor := NewCodexExecutor(&config.Config{})
				auth := &cliproxyauth.Auth{
					ID: "effort-loopback-only", Provider: "codex", Status: cliproxyauth.StatusActive,
					Attributes: map[string]string{"base_url": server.URL + "/backend-api/codex"},
					Metadata:   map[string]any{"access_token": "fake-loopback-token", "account_id": "test-account"},
				}
				payload := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","stream":%t,"max_tokens":64,"messages":[{"role":"user","content":"Reply PONG"}],%s}`, streaming, tc.fields))
				req := cliproxyexecutor.Request{Model: "gpt-6.1-sol", Payload: payload, Format: sdktranslator.FromString("claude")}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude"), Stream: streaming}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if streaming {
					stream, err := executor.ExecuteStream(ctx, auth, req, opts)
					if err != nil {
						t.Fatalf("ExecuteStream: %v", err)
					}
					var response strings.Builder
					for chunk := range stream.Chunks {
						if chunk.Err != nil {
							t.Fatalf("stream chunk: %v", chunk.Err)
						}
						response.Write(chunk.Payload)
					}
					if !strings.Contains(response.String(), `"text":"PONG"`) {
						t.Fatalf("missing translated PONG: %s", response.String())
					}
				} else {
					response, err := executor.Execute(ctx, auth, req, opts)
					if err != nil {
						t.Fatalf("Execute: %v", err)
					}
					if got := gjson.GetBytes(response.Payload, "content.0.text").String(); got != "PONG" {
						t.Fatalf("translated response text = %q, want PONG", got)
					}
				}
				select {
				case body := <-requests:
					if got := gjson.GetBytes(body, "reasoning.effort").String(); got != tc.want {
						t.Errorf("upstream reasoning.effort = %q, want %q", got, tc.want)
					}
					if !gjson.GetBytes(body, "stream").Bool() {
						t.Error("Codex upstream must remain streaming")
					}
				default:
					t.Fatal("no request reached loopback upstream")
				}
			})
		}
	}
}
