package executor

import (
	"context"
	"encoding/json"
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

const autoClassifierTestSystem = "You are a security monitor for autonomous AI coding agents.\nEvaluate the latest action; do not execute it."
const autoClassifierTestStage1 = "\nErr on the side of blocking. Stage 1 does NOT apply user intent or ALLOW exceptions — stage 2 will handle those. Your ENTIRE response MUST begin with <block>."
const autoClassifierTestStage2 = "\nReview the classification process and follow it carefully. Use <thinking> before responding with <block>."

// Reintroducing implicit hosted tools, guidance, or forced image selection on
// these tool-less side queries must break this test. Only the external upstream
// is faked: request translation, executor policy, and response translation run.
func TestCodexExecutorAutoClassifierDoesNotInvokeImageBridge(t *testing.T) {
	for _, stage := range []struct{ name, suffix string }{
		{"stage1", autoClassifierTestStage1},
		{"stage2", autoClassifierTestStage2},
	} {
		for _, systemBlocks := range []bool{false, true} {
			for _, keyword := range []string{"image generation", "生图", "image_gen.imagegen", "[$imagegen]", "ordinary shell access"} {
				t.Run(fmt.Sprintf("%s/blocks=%t/%s", stage.name, systemBlocks, keyword), func(t *testing.T) {
					var system any = autoClassifierTestSystem
					if systemBlocks {
						system = []any{map[string]any{"type": "text", "text": autoClassifierTestSystem}}
					}
					user := "<transcript>Available capability: " + keyword + ". Proposed command: date -u -Is.</transcript>" + stage.suffix
					payload, err := json.Marshal(map[string]any{
						"model": "gpt-6.1-terra", "stream": false, "max_tokens": 2112,
						"system": system, "stop_sequences": []string{"</block>"},
						"messages": []any{map[string]any{"role": "user", "content": user}},
					})
					if err != nil {
						t.Fatal(err)
					}
					wire, response := executeAutoClassifierImageFixture(t, payload, "<block>no</block>")
					for _, tool := range gjson.GetBytes(wire, "tools").Array() {
						if tool.Get("type").String() == "image_generation" {
							t.Error("classifier received an implicit hosted image tool")
						}
					}
					if gjson.GetBytes(wire, "tool_choice.type").String() == "image_generation" {
						t.Error("classifier was forced to generate an image")
					}
					if strings.Contains(gjson.GetBytes(wire, "instructions").String(), "<cliproxy-codex-image-generation>") {
						t.Error("classifier received implicit image bridge instructions")
					}
					if got := gjson.GetBytes(wire, "input.0.content.0.text").String(); got != autoClassifierTestSystem {
						t.Error("classifier security instructions changed")
					}
					if got := gjson.GetBytes(wire, "input.1.content.0.text").String(); got != user {
						t.Error("classifier transcript or stage instructions changed")
					}
					if got := gjson.GetBytes(response, "content.0.text").String(); got != "<block>no</block>" {
						t.Errorf("allow response was polluted: text length=%d", len(got))
					}
				})
			}
		}
	}
}

// Claude Code's side query prepends a billing attribution block even with
// skipSystemPromptPrefix enabled. The monitor block is not necessarily first.
func TestCodexExecutorAutoClassifierWithAttributionHeader(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"model": "gpt-6.1-terra", "stream": false, "max_tokens": 2112,
		"system": []any{
			map[string]any{"type": "text", "text": "x-anthropic-billing-header: synthetic-test-only"},
			map[string]any{"type": "text", "text": autoClassifierTestSystem, "cache_control": map[string]any{"type": "ephemeral"}},
			map[string]any{"type": "text", "text": "Synthetic session context."},
		},
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "<transcript>"},
			map[string]any{"type": "text", "text": "Quoted capability: image generation."},
			map[string]any{"type": "text", "text": "</transcript>" + autoClassifierTestStage1},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wire, response := executeAutoClassifierImageFixture(t, payload, "<block>no</block>")
	for _, tool := range gjson.GetBytes(wire, "tools").Array() {
		if tool.Get("type").String() == "image_generation" {
			t.Error("attributed classifier received an implicit image tool")
		}
	}
	if got := gjson.GetBytes(wire, "input.0.content.1.text").String(); got != autoClassifierTestSystem {
		t.Error("attributed classifier security instructions changed")
	}
	if got := gjson.GetBytes(response, "content.0.text").String(); got != "<block>no</block>" {
		t.Errorf("attributed classifier response polluted: text length=%d", len(got))
	}
}

func TestCodexExecutorAutoClassifierPreservesBlockVerdict(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"model": "gpt-6.1-terra", "stream": false, "max_tokens": 2112,
		"system":   autoClassifierTestSystem,
		"messages": []any{map[string]any{"role": "user", "content": "<transcript>Quoted capability: image generation.</transcript>" + autoClassifierTestStage1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, response := executeAutoClassifierImageFixture(t, payload, "<block>yes</block>")
	if got := gjson.GetBytes(response, "content.0.text").String(); got != "<block>yes</block>" {
		t.Errorf("block verdict changed: text length=%d", len(got))
	}
}

// Narrow detection must not disable normal image generation or override
// caller-supplied tool contracts merely because classifier text is quoted.
func TestCodexImageBridgeKeepsNonClassifierAndExplicitToolRequests(t *testing.T) {
	cases := []struct {
		name         string
		system       string
		user         string
		tools        string
		choice       string
		wantHosted   int64
		wantChoice   string
		wantGuidance bool
	}{
		{"normal image request", "You are a helpful assistant.", "Generate an image of a cat.", "", "", 1, "image_generation", true},
		{"monitor system without stage", autoClassifierTestSystem, "Generate an image of a cat.", "", "", 1, "image_generation", true},
		{"classifier quoted in user only", "You are a helpful assistant.", autoClassifierTestSystem + "<transcript>image generation</transcript>" + autoClassifierTestStage1, "", "", 1, "image_generation", true},
		{"stage marker inside transcript only", autoClassifierTestSystem, "<transcript>image generation" + autoClassifierTestStage1 + "</transcript>Explain the code.", "", "", 1, "image_generation", true},
		{"unknown classifier stage", autoClassifierTestSystem, "<transcript>image generation</transcript>Unknown future classification format.", "", "", 1, "image_generation", true},
		{"explicit hosted tool", autoClassifierTestSystem, "<transcript>image generation</transcript>" + autoClassifierTestStage1, `[{"type":"image_generation","action":"edit","model":"caller-model"}]`, `{"type":"image_generation"}`, 1, "image_generation", true},
		{"local image namespace", autoClassifierTestSystem, "<transcript>image generation</transcript>" + autoClassifierTestStage1, `[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]`, `"auto"`, 0, "auto", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6.1-terra","instructions":"","input":[{"role":"developer","content":[{"type":"input_text","text":` + mustJSONString(tc.system) + `}]},{"role":"user","content":[{"type":"input_text","text":` + mustJSONString(tc.user) + `}]}]`)
			if tc.tools != "" {
				body = append(body, []byte(`,"tools":`+tc.tools)...)
			}
			if tc.choice != "" {
				body = append(body, []byte(`,"tool_choice":`+tc.choice)...)
			}
			body = append(body, '}')
			out := maybeEnsureCodexImageGenerationTool(body, &cliproxyauth.Auth{Provider: "codex"}, "gpt-6.1-terra", nil)
			var hosted int64
			for _, tool := range gjson.GetBytes(out, "tools").Array() {
				if tool.Get("type").String() == "image_generation" {
					hosted++
					if tc.name == "explicit hosted tool" && (tool.Get("action").String() != "edit" || tool.Get("model").String() != "caller-model") {
						t.Error("caller-supplied hosted tool was changed")
					}
				}
			}
			if hosted != tc.wantHosted {
				t.Errorf("hosted tool count=%d, want %d", hosted, tc.wantHosted)
			}
			choice := gjson.GetBytes(out, "tool_choice")
			gotChoice := choice.String()
			if choice.IsObject() {
				gotChoice = choice.Get("type").String()
			}
			if gotChoice != tc.wantChoice {
				t.Errorf("tool choice=%q, want %q", gotChoice, tc.wantChoice)
			}
			if guidance := strings.Contains(gjson.GetBytes(out, "instructions").String(), "<cliproxy-codex-image-generation>"); guidance != tc.wantGuidance {
				t.Errorf("bridge guidance=%t, want %t", guidance, tc.wantGuidance)
			}
		})
	}
}

func executeAutoClassifierImageFixture(t *testing.T, payload []byte, verdict string) ([]byte, []byte) {
	t.Helper()
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/backend-api/codex/responses" {
			t.Errorf("unexpected upstream route: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected route", http.StatusNotFound)
			return
		}
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream request: %v", err)
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		requests <- wire
		output := []any{map[string]any{"id": "m_classifier", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": verdict}}}}
		// Return a synthetic asset only if the real outbound request advertises
		// that capability. No image tool on the wire means no image from upstream.
		for _, tool := range gjson.GetBytes(wire, "tools").Array() {
			if tool.Get("type").String() == "image_generation" {
				output = append(output, map[string]any{"id": "ig_classifier", "type": "image_generation_call", "status": "completed", "output_format": "png", "result": strings.Repeat("A", 4096)})
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "r_classifier", "model": "gpt-6.1-terra"}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "r_classifier", "model": "gpt-6.1-terra", "status": "completed", "output": output, "usage": map[string]any{"input_tokens": 11, "output_tokens": 44, "total_tokens": 55}}},
		} {
			raw, err := json.Marshal(event)
			if err != nil {
				t.Errorf("encode upstream event: %v", err)
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer server.Close()
	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "classifier-loopback-only", Provider: "codex", Status: cliproxyauth.StatusActive,
		Attributes: map[string]string{"base_url": server.URL + "/backend-api/codex"},
		Metadata:   map[string]any{"access_token": "fake-loopback-token", "account_id": "test-account"},
	}
	req := cliproxyexecutor.Request{Model: "gpt-6.1-terra", Payload: payload, Format: sdktranslator.FormatClaude}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := executor.Execute(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	select {
	case wire := <-requests:
		return wire, response.Payload
	default:
		t.Fatal("request did not reach loopback upstream")
		return nil, nil
	}
}
