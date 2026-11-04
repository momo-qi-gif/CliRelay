package claude

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertClaudeRequestToCodex_MidConversationSystemRole(t *testing.T) {
	for _, content := range []string{`"Keep responses brief."`, `[{"type":"text","text":"Keep responses brief."}]`} {
		input := []byte(`{"system":[{"type":"text","text":"Top-level instruction."}],"messages":[{"role":"user","content":"Hello"},{"role":"system","content":` + content + `},{"role":"assistant","content":"Hi"}]}`)
		out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)
		messages := gjson.GetBytes(out, "input").Array()
		if len(messages) != 4 {
			t.Fatalf("expected four messages, got %s", out)
		}
		for i, role := range []string{"developer", "user", "developer", "assistant"} {
			if got := messages[i].Get("role").String(); got != role {
				t.Errorf("message %d: role = %q, want %q", i, got, role)
			}
		}
		if got := messages[2].Get("content.0.text").String(); got != "Keep responses brief." {
			t.Errorf("system text changed: %q", got)
		}
	}
}

func TestConvertClaudeRequestToCodex_StripsDeferLoading(t *testing.T) {
	input := []byte(`{
		"model": "claude-sonnet-4-6",
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [
			{"name": "WebSearch", "description": "search", "input_schema": {"type": "object"}, "defer_loading": true},
			{"name": "Read", "description": "read", "input_schema": {"type": "object"}}
		]
	}`)

	out := ConvertClaudeRequestToCodex("gpt-5.2", input, true)

	// Ensure defer_loading is removed from all tools in the output
	tools := gjson.GetBytes(out, "tools")
	if !tools.IsArray() {
		t.Fatal("expected tools to be an array")
	}
	tools.ForEach(func(i, tool gjson.Result) bool {
		if tool.Get("defer_loading").Exists() {
			t.Fatalf("tool %d still has defer_loading: %s", i.Int(), tool.Raw)
		}
		return true
	})
}

func TestConvertClaudeRequestToCodex_EmptyToolsOmitsToolChoice(t *testing.T) {
	input := []byte(`{
		"model":"claude-haiku-4-5",
		"messages":[{"role":"user","content":"Create a title"}],
		"tools":[],
		"tool_choice":{"type":"auto"}
	}`)

	out := ConvertClaudeRequestToCodex("grok-4", input, false)
	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("empty tools must not emit tool_choice: %s", out)
	}
	if tools := gjson.GetBytes(out, "tools"); tools.Exists() && len(tools.Array()) != 0 {
		t.Fatalf("unexpected translated tools: %s", out)
	}
}
