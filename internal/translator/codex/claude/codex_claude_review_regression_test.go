package claude

import (
	"github.com/tidwall/gjson"
	"strings"
	"testing"
)

func TestReviewTruncatedToolNotCompleted(t *testing.T) {
	for _, event := range []string{"response.completed", "response.incomplete"} {
		chunks := []string{
			`data: {"type":"response.created","response":{"id":"r1"}}`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"Read"}}`,
			`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"f1","delta":"{\"path\":"}`,
			`data: {"type":"` + event + `","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{}}}`,
		}
		outputs := convertCodexClaudeStream(t, nil, chunks)
		assertClaudeContentBlockLifecycle(t, outputs)
		joined := strings.Join(outputs, "")
		if strings.Contains(joined, `"type":"tool_use"`) {
			t.Fatalf("truncated tool exposed: %s", joined)
		}
		if !strings.Contains(joined, `"stop_reason":"max_tokens"`) {
			t.Fatal(joined)
		}
	}
}

func TestReviewDeferredContentOrder(t *testing.T) {
	chunks := []string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"Read"}}`,
		`data: {"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"FIRST"}`,
		`data: {"type":"response.reasoning_summary_part.added","output_index":2}`,
		`data: {"type":"response.reasoning_summary_text.delta","output_index":2,"delta":"SECOND"}`,
		`data: {"type":"response.reasoning_summary_part.done","output_index":2}`,
		`data: {"type":"response.output_text.delta","output_index":3,"content_index":0,"delta":"THIRD"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"Read","arguments":"{}"}}`,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
	}
	outputs := convertCodexClaudeStream(t, nil, chunks)
	assertClaudeContentBlockLifecycle(t, outputs)
	var values []string
	for _, line := range strings.Split(strings.Join(outputs, ""), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		v := gjson.Parse(line[6:])
		if v.Get("delta.type").String() == "text_delta" {
			values = append(values, v.Get("delta.text").String())
		}
		if v.Get("delta.type").String() == "thinking_delta" {
			values = append(values, v.Get("delta.thinking").String())
		}
	}
	if strings.Join(values, ",") != "FIRST,SECOND,THIRD" {
		t.Fatalf("content reordered/lost: %v", values)
	}
}

func TestReviewTerminalSnapshotAfterDeferredDelta(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"Read"}}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"hello "}`,
		`{"type":"response.completed","response":{"output":[{"id":"f1","type":"function_call","call_id":"c1","name":"Read","arguments":"{}"},{"type":"message","content":[{"type":"output_text","text":"hello world"}]}],"usage":{}}}`,
	}
	text, _ := repairStreamResult(t, events)
	if text != "hello world" {
		t.Fatalf("terminal/deferred content duplicated or reordered: %q", text)
	}
}

func TestReviewIdentitylessSequentialTool(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"Read"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"f1","call_id":"c1","name":"Read","arguments":"{}"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"f2","call_id":"c2","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"path\":\"README\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"f2","call_id":"c2","name":"Read"}}`,
		`{"type":"response.completed","response":{"usage":{}}}`,
	}
	_, tools := repairStreamResult(t, events)
	if tools["c2"] != `{"path":"README"}` {
		t.Fatalf("identityless active tool lost: %#v", tools)
	}
}
