package claude

import (
	"context"
	"fmt"
	"github.com/tidwall/gjson"
	"strings"
	"testing"
)

func TestCodexClaudeRefusalPreserved(t *testing.T) {
	const refusal = "I cannot help with that."
	terminal := `{"type":"response.completed","response":{"id":"r1","model":"gpt-6.1-sol","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"I cannot help with that."}]}],"usage":{"input_tokens":10,"output_tokens":6}}}`
	t.Run("nonstream", func(t *testing.T) {
		out := ConvertCodexResponseToClaudeNonStream(context.Background(), "gpt-6.1-sol", nil, nil, []byte(terminal), nil)
		if gjson.Get(out, "content.0.text").String() != refusal {
			t.Fatalf("refusal lost: %s", out)
		}
	})
	for _, tc := range []struct {
		name   string
		events []string
	}{
		{"delta then done", []string{
			`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":""}}`,
			`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"I cannot help "}`,
			`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"with that."}`,
			`{"type":"response.refusal.done","output_index":0,"content_index":0,"refusal":"I cannot help with that."}`,
			`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":"I cannot help with that."}}`,
		}},
		{"partial delta then snapshot", []string{
			`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"I cannot help "}`,
		}},
		{"done only", []string{`{"type":"response.refusal.done","output_index":0,"content_index":0,"refusal":"I cannot help with that."}`}},
		{"part done only", []string{`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":"I cannot help with that."}}`}},
		{"terminal only", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := []string{`data: {"type":"response.created","response":{"id":"r1","model":"gpt-6.1-sol"}}`}
			for _, e := range tc.events {
				chunks = append(chunks, "data: "+e)
			}
			chunks = append(chunks, "data: "+terminal, "data: "+terminal)
			outputs := convertCodexClaudeStream(t, nil, chunks)
			assertClaudeContentBlockLifecycle(t, outputs)
			var text strings.Builder
			joined := strings.Join(outputs, "")
			for _, line := range strings.Split(joined, "\n") {
				if strings.HasPrefix(line, "data: ") {
					v := gjson.Parse(line[6:])
					if v.Get("delta.type").String() == "text_delta" {
						text.WriteString(v.Get("delta.text").String())
					}
				}
			}
			if text.String() != refusal {
				t.Fatalf("refusal missing/duplicated = %q\n%s", text.String(), joined)
			}
			if strings.Count(joined, `"type":"message_stop"`) != 1 {
				t.Fatal("terminal duplicated")
			}
		})
	}
}

func TestCodexClaudeMaxOutputTerminal(t *testing.T) {
	for _, eventType := range []string{"response.completed", "response.incomplete"} {
		t.Run(eventType, func(t *testing.T) {
			terminal := fmt.Sprintf(`{"type":%q,"response":{"id":"r1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]},{"type":"function_call","id":"fc1","call_id":"c1","name":"Read","arguments":"{\"path\":"}],"usage":{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":3}}}}`, eventType)
			out := ConvertCodexResponseToClaudeNonStream(context.Background(), "gpt-6.1-sol", nil, nil, []byte(terminal), nil)
			if got := gjson.Get(out, "stop_reason").String(); got != "max_tokens" {
				t.Fatalf("reason=%q: %s", got, out)
			}
			if gjson.Get(out, "content.0.text").String() != "partial" || gjson.Get(out, "usage.input_tokens").Int() != 7 {
				t.Fatal(out)
			}
			// Truncation must never ask Claude Code to execute a half-generated tool.
			if strings.Contains(out, `"type":"tool_use"`) {
				t.Fatal("truncated tool was emitted: " + out)
			}
			chunks := []string{
				`data: {"type":"response.created","response":{"id":"r1","model":"gpt-6.1-sol"}}`,
				`data: {"type":"response.output_text.delta","delta":"partial"}`,
				`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read"}}`,
				`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc1","delta":"{\"path\":"}`,
				"data: " + terminal,
			}
			outputs := convertCodexClaudeStream(t, nil, chunks)
			assertClaudeContentBlockLifecycle(t, outputs)
			joined := strings.Join(outputs, "")
			if !strings.Contains(joined, `"stop_reason":"max_tokens"`) || !strings.Contains(joined, `"cache_read_input_tokens":3`) {
				t.Fatal(joined)
			}
		})
	}
}
