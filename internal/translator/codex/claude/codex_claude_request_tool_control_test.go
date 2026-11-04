package claude

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertClaudeRequestToCodex_ToolControlModes(t *testing.T) {
	tests := []struct {
		name       string
		choice     string
		wantChoice string
	}{
		{name: "auto", choice: `{"type":"auto"}`, wantChoice: `"auto"`},
		{name: "any", choice: `{"type":"any"}`, wantChoice: `"required"`},
		{name: "none", choice: `{"type":"none"}`, wantChoice: `"none"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":` + tt.choice + `}`)
			out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)
			if got := gjson.GetBytes(out, "tool_choice").Raw; got != tt.wantChoice {
				t.Fatalf("tool_choice = %s, want %s; translated=%s", got, tt.wantChoice, out)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ForcedToolUsesSharedShortName(t *testing.T) {
	longName := "mcp__package__" + "read_file_" + "01234567890123456789012345678901234567890123456789"
	input := []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"` + longName + `","input":{}}]}],"tools":[{"name":"` + longName + `","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"` + longName + `"}}`)
	out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)
	toolName := gjson.GetBytes(out, "tools.0.name").String()
	if toolName == "" {
		t.Fatalf("translated tool has no name: %s", out)
	}
	if toolName == longName {
		t.Fatalf("tool name was not shortened: %q", toolName)
	}
	for path, got := range map[string]string{
		"tool_choice.name": gjson.GetBytes(out, "tool_choice.name").String(),
		"input.0.name":     gjson.GetBytes(out, "input.0.name").String(),
	} {
		if got != toolName {
			t.Errorf("%s = %q, want shared short name %q; translated=%s", path, got, toolName, out)
		}
	}
	if got := gjson.GetBytes(out, "tool_choice.type").String(); got != "function" {
		t.Errorf("tool_choice.type = %q, want function; translated=%s", got, out)
	}
}

func TestConvertClaudeRequestToCodex_DisableParallelToolUse(t *testing.T) {
	tests := []struct {
		name     string
		parallel string
		want     bool
	}{
		{name: "disabled", parallel: `,"disable_parallel_tool_use":true`, want: false},
		{name: "enabled", parallel: `,"disable_parallel_tool_use":false`, want: true},
		{name: "default", want: true},
		{name: "invalid type", parallel: `,"disable_parallel_tool_use":"true"`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":{"type":"auto"` + tt.parallel + `}}`)
			out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)
			if got := gjson.GetBytes(out, "parallel_tool_calls").Bool(); got != tt.want {
				t.Fatalf("parallel_tool_calls = %t, want %t; translated=%s", got, tt.want, out)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ForcedWebSearchUsesNativeToolChoice(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":"find this"}],"tools":[{"type":"web_search_20250305","name":"web_search"}],"tool_choice":{"type":"tool","name":"web_search"}}`)
	out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)
	if got := gjson.GetBytes(out, "tools.0.type").String(); got != "web_search" {
		t.Fatalf("web search tool type = %q, want web_search; translated=%s", got, out)
	}
	choice := gjson.GetBytes(out, "tool_choice")
	if got := choice.Get("type").String(); got != "web_search" {
		t.Fatalf("tool_choice.type = %q, want web_search; translated=%s", got, out)
	}
	if choice.Get("name").Exists() {
		t.Fatalf("native web_search choice must not have a function name: %s", out)
	}
}

func TestConvertClaudeRequestToCodex_InvalidToolChoiceFallsBackToAuto(t *testing.T) {
	tests := []struct {
		name   string
		choice string
	}{
		{name: "unknown mode", choice: `{"type":"sometimes"}`},
		{name: "missing tool name", choice: `{"type":"tool"}`},
		{name: "unknown tool name", choice: `{"type":"tool","name":"Missing"}`},
		{name: "wrong shape", choice: `"required"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":` + tt.choice + `}`)
			out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)
			if got := gjson.GetBytes(out, "tool_choice").Raw; got != `"auto"` {
				t.Fatalf("tool_choice = %s, want legacy auto; translated=%s", got, out)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_NoToolsOmitsToolChoice(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[],"tool_choice":{"type":"any","disable_parallel_tool_use":true}}`)
	out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)
	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("request without tools must omit tool_choice: %s", out)
	}
}
