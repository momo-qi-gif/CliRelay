package claude

import (
	"encoding/json"
	"fmt"
	"github.com/tidwall/gjson"
	"strings"
	"testing"
)

func repairStreamResult(t *testing.T, events []string) (string, map[string]string) {
	t.Helper()
	chunks := []string{`data: {"type":"response.created","response":{"id":"r1","model":"gpt-6.1-sol"}}`}
	for _, event := range events {
		chunks = append(chunks, "data: "+event)
	}
	outputs := convertCodexClaudeStream(t, []byte(`{"tools":[{"name":"Read"}]}`), chunks)
	assertClaudeContentBlockLifecycle(t, outputs)
	names := map[int]string{}
	tools := map[string]string{}
	var text strings.Builder
	for _, line := range strings.Split(strings.Join(outputs, ""), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		v := gjson.Parse(line[6:])
		idx := int(v.Get("index").Int())
		if v.Get("type").String() == "content_block_start" && v.Get("content_block.type").String() == "tool_use" {
			names[idx] = v.Get("content_block.id").String()
		}
		if v.Get("delta.type").String() == "input_json_delta" {
			tools[names[idx]] += v.Get("delta.partial_json").String()
		}
		if v.Get("delta.type").String() == "text_delta" {
			text.WriteString(v.Get("delta.text").String())
		}
	}
	return text.String(), tools
}

func TestClaudeStreamFinalSnapshots(t *testing.T) {
	for _, kind := range []string{"output_text", "refusal"} {
		for _, mode := range []string{"done", "item", "terminal", "delta_all_snapshots"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				field := "text"
				if kind == "refusal" {
					field = "refusal"
				}
				part := fmt.Sprintf(`{"type":%q,%q:"hello world"}`, kind, field)
				item := `{"id":"msg1","type":"message","content":[` + part + `]}`
				var events []string
				if mode == "delta_all_snapshots" {
					events = append(events, fmt.Sprintf(`{"type":%q,"item_id":"msg1","output_index":0,"content_index":0,"delta":"hello "}`, "response."+kind+".delta"))
				}
				if mode == "done" || mode == "delta_all_snapshots" {
					events = append(events, fmt.Sprintf(`{"type":%q,"item_id":"msg1","output_index":0,"content_index":0,%q:"hello world"}`, "response."+kind+".done", field))
				}
				if mode == "item" || mode == "delta_all_snapshots" {
					events = append(events, `{"type":"response.output_item.done","output_index":0,"item":`+item+`}`)
				}
				output := "[]"
				if mode == "terminal" || mode == "delta_all_snapshots" {
					output = "[" + item + "]"
				}
				terminal := `{"type":"response.completed","response":{"output":` + output + `,"usage":{}}}`
				events = append(events, terminal, terminal)
				text, _ := repairStreamResult(t, events)
				if text != "hello world" {
					t.Fatalf("text missing/duplicated: %q", text)
				}
			})
		}
	}
}

func TestClaudeStreamInterleavedTools(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc1","delta":"{\"x\":"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc2","call_id":"c2","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc2","delta":"{\"y\":2}"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc1","delta":"1}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc2","call_id":"c2","name":"Read","arguments":"{\"y\":2}"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read","arguments":"{\"x\":1}"}}`,
		`{"type":"response.completed","response":{"usage":{}}}`,
	}
	_, tools := repairStreamResult(t, events)
	if tools["c1"] != `{"x":1}` || tools["c2"] != `{"y":2}` {
		t.Fatalf("arguments lost: %#v", tools)
	}
	for _, args := range tools {
		if !json.Valid([]byte(args)) {
			t.Fatalf("invalid JSON: %q", args)
		}
	}
}

func TestClaudeStreamTextDuringToolArguments(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc1","delta":"{\"x\":"}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"progress"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc1","delta":"1}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read","arguments":"{\"x\":1}"}}`,
		`{"type":"response.completed","response":{"output":[{"id":"fc1","type":"function_call","call_id":"c1","name":"Read","arguments":"{\"x\":1}"},{"type":"message","content":[{"type":"output_text","text":"progress"}]}],"usage":{}}}`,
	}
	text, tools := repairStreamResult(t, events)
	if text != "progress" || tools["c1"] != `{"x":1}` {
		t.Fatalf("interleaved output lost: %q %#v", text, tools)
	}
}

func TestClaudeStreamThinkingDuringToolArguments(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc1","delta":"{\"x\":"}`,
		`{"type":"response.reasoning_summary_part.added","output_index":1}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":1,"delta":"explanation"}`,
		`{"type":"response.reasoning_summary_part.done","output_index":1}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc1","delta":"1}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read","arguments":"{\"x\":1}"}}`,
		`{"type":"response.completed","response":{"usage":{}}}`,
	}
	chunks := []string{}
	for _, event := range events {
		chunks = append(chunks, "data: "+event)
	}
	outputs := convertCodexClaudeStream(t, nil, chunks)
	assertClaudeContentBlockLifecycle(t, outputs)
	if !strings.Contains(strings.Join(outputs, ""), `"thinking":"explanation"`) {
		t.Fatal("thinking lost")
	}
}

func TestClaudeStreamSnapshotToolArgumentsDeduplicated(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc1","delta":"{\"x\":"}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc1","arguments":"{\"x\":1}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"c1","name":"Read","arguments":"{\"x\":1}"}}`,
		`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc1","call_id":"c1","name":"Read","arguments":"{\"x\":1}"}],"usage":{}}}`,
	}
	_, tools := repairStreamResult(t, events)
	if tools["c1"] != `{"x":1}` {
		t.Fatalf("snapshot suffix lost/duplicated: %#v", tools)
	}
}
