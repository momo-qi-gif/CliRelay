package executor

import (
	"context"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCodexExecutorClaudeTerminalEvents(t *testing.T) {
	for _, tc := range []struct {
		name, event, status, reason string
		wantError                   bool
	}{
		{"length incomplete", "response.incomplete", "incomplete", "max_output_tokens", false},
		{"length completed envelope", "response.completed", "incomplete", "max_output_tokens", false},
		{"content filter", "response.incomplete", "incomplete", "content_filter", true},
		{"unknown incomplete", "response.incomplete", "incomplete", "unrecognized_reason", true},
		{"failed", "response.failed", "failed", "", true},
		{"missing terminal", "", "", "", true},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, streaming), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-6.1-sol\"}}\n\n")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"PONG\"}\n\n")
					// Terminal output can be empty: non-stream must retain output_item.done.
					_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg1","type":"message","role":"assistant","content":[{"type":"output_text","text":"PONG"}]}}`+"\n\n")
					if tc.event != "" {
						_, _ = fmt.Fprintf(w, "data: {\"type\":%q,\"response\":{\"id\":\"r1\",\"status\":%q,\"incomplete_details\":{\"reason\":%q},\"error\":{\"code\":\"test_error\",\"message\":\"fixture failure\"},\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n", tc.event, tc.status, tc.reason)
					}
				}))
				defer upstream.Close()
				e := NewCodexExecutor(&config.Config{})
				auth := &cliproxyauth.Auth{ID: "terminal-loopback", Provider: "codex", Status: cliproxyauth.StatusActive, Attributes: map[string]string{"base_url": upstream.URL + "/backend-api/codex"}, Metadata: map[string]any{"access_token": "fake-loopback"}}
				req := cliproxyexecutor.Request{Model: "gpt-6.1-sol", Payload: []byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"Reply PONG"}]}`)}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, Stream: streaming}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var output string
				var gotError error
				if streaming {
					result, err := e.ExecuteStream(ctx, auth, req, opts)
					gotError = err
					if err == nil {
						for chunk := range result.Chunks {
							output += string(chunk.Payload)
							if chunk.Err != nil {
								gotError = chunk.Err
							}
						}
					}
				} else {
					result, err := e.Execute(ctx, auth, req, opts)
					gotError = err
					output = string(result.Payload)
				}
				if tc.wantError {
					if gotError == nil {
						t.Fatalf("terminal failure was silently accepted: %s", output)
					}
					if tc.reason != "" && (strings.Contains(gotError.Error(), "stream disconnected") || strings.Contains(gotError.Error(), "response_stream_incomplete")) {
						t.Fatalf("known incomplete reason misreported as transport loss: %v", gotError)
					}
					if streaming && strings.Contains(output, `"type":"message_stop"`) {
						t.Fatalf("failed terminal emitted successful message_stop: %s", output)
					}
					return
				}
				if gotError != nil {
					t.Fatalf("valid truncation returned error: %v", gotError)
				}
				if !strings.Contains(output, "PONG") {
					t.Fatalf("partial output lost: %s", output)
				}
				if streaming {
					if !strings.Contains(output, `"stop_reason":"max_tokens"`) || !strings.Contains(output, `"type":"message_stop"`) {
						t.Fatal(output)
					}
				} else {
					if gjson.Get(output, "stop_reason").String() != "max_tokens" || gjson.Get(output, "usage.output_tokens").Int() != 2 {
						t.Fatal(output)
					}
				}
			})
		}
	}
}
