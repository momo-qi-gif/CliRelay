// Package claude provides response translation functionality for Codex to Claude Code API compatibility.
// This package handles the conversion of Codex API responses into Claude Code-compatible
// Server-Sent Events (SSE) format, implementing a sophisticated state machine that manages
// different response types including text content, thinking processes, and function calls.
// The translation ensures proper sequencing of SSE events and maintains state across
// multiple response chunks to provide a seamless streaming experience.
package claude

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	dataTag = []byte("data:")
)

// ConvertCodexResponseToClaudeParams holds parameters for response conversion.
type ConvertCodexResponseToClaudeParams struct {
	HasToolCall       bool
	BlockIndex        int
	OpenBlockType     string
	OpenBlockIndex    int
	MessageCompleted  bool
	RefusalTextByPart map[string]string
	TextByPart        map[string]string
	Tools             []*codexClaudePendingTool
	DeferredEvents    [][]byte
	ReplayingDeferred bool
}

const (
	codexClaudeThinkingBlock = "thinking"
	codexClaudeTextBlock     = "text"
	codexClaudeToolBlock     = "tool_use"
)

// ConvertCodexResponseToClaude performs sophisticated streaming response format conversion.
// This function implements a complex state machine that translates Codex API responses
// into Claude Code-compatible Server-Sent Events (SSE) format. It manages different response types
// and handles state transitions between content blocks, thinking processes, and function calls.
//
// Response type states: 0=none, 1=content, 2=thinking, 3=function
// The function maintains state across multiple calls to ensure proper SSE event sequencing.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response (unused in current implementation)
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for maintaining state between calls
//
// Returns:
//   - []string: A slice of strings, each containing a Claude Code-compatible JSON response
func ConvertCodexResponseToClaude(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []string {
	if *param == nil {
		*param = &ConvertCodexResponseToClaudeParams{
			HasToolCall: false,
			BlockIndex:  0,
		}
	}
	params := (*param).(*ConvertCodexResponseToClaudeParams)
	if params.MessageCompleted {
		return []string{}
	}

	// log.Debugf("rawJSON: %s", string(rawJSON))
	if !bytes.HasPrefix(rawJSON, dataTag) {
		return []string{}
	}
	rawJSON = bytes.TrimSpace(rawJSON[5:])

	output := ""
	rootResult := gjson.ParseBytes(rawJSON)
	typeResult := rootResult.Get("type")
	typeStr := typeResult.String()
	if !params.ReplayingDeferred && codexClaudeHasPendingTool(params) &&
		(strings.HasPrefix(typeStr, "response.reasoning_summary_") ||
			strings.HasPrefix(typeStr, "response.output_text.") || strings.HasPrefix(typeStr, "response.refusal.") ||
			strings.HasPrefix(typeStr, "response.content_part.")) {
		params.DeferredEvents = append(params.DeferredEvents, append([]byte("data: "), rawJSON...))
		return []string{}
	}
	template := ""
	if typeStr == "response.created" {
		template = `{"type":"message_start","message":{"id":"","type":"message","role":"assistant","model":"claude-opus-4-1-20250805","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0},"content":[],"stop_reason":null}}`
		template, _ = sjson.Set(template, "message.model", rootResult.Get("response.model").String())
		template, _ = sjson.Set(template, "message.id", rootResult.Get("response.id").String())

		output = "event: message_start\n"
		output += fmt.Sprintf("data: %s\n\n", template)
	} else if typeStr == "response.reasoning_summary_part.added" {
		if params.OpenBlockType == codexClaudeThinkingBlock {
			return []string{output}
		}
		output += closeCodexClaudeContentBlock(params)
		template = `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`
		template, _ = sjson.Set(template, "index", params.BlockIndex)
		openCodexClaudeContentBlock(params, codexClaudeThinkingBlock)

		output += "event: content_block_start\n"
		output += fmt.Sprintf("data: %s\n\n", template)
	} else if typeStr == "response.reasoning_summary_text.delta" {
		if params.OpenBlockType == "" {
			template = `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`
			template, _ = sjson.Set(template, "index", params.BlockIndex)
			openCodexClaudeContentBlock(params, codexClaudeThinkingBlock)
			output += "event: content_block_start\n"
			output += fmt.Sprintf("data: %s\n\n", template)
		}
		if params.OpenBlockType != codexClaudeThinkingBlock {
			return []string{output}
		}
		template = `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`
		template, _ = sjson.Set(template, "index", params.OpenBlockIndex)
		template, _ = sjson.Set(template, "delta.thinking", rootResult.Get("delta").String())

		output += "event: content_block_delta\n"
		output += fmt.Sprintf("data: %s\n\n", template)
	} else if typeStr == "response.reasoning_summary_part.done" {
		if params.OpenBlockType == codexClaudeThinkingBlock {
			output += closeCodexClaudeContentBlock(params)
		}
	} else if typeStr == "response.content_part.added" {
		if params.OpenBlockType == codexClaudeToolBlock {
			return []string{}
		}
		if params.OpenBlockType == codexClaudeTextBlock {
			return []string{output}
		}
		output += closeCodexClaudeContentBlock(params)
		template = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
		template, _ = sjson.Set(template, "index", params.BlockIndex)
		openCodexClaudeContentBlock(params, codexClaudeTextBlock)

		output += "event: content_block_start\n"
		output += fmt.Sprintf("data: %s\n\n", template)
	} else if typeStr == "response.refusal.delta" || typeStr == "response.refusal.done" {
		text := rootResult.Get("delta").String()
		full := typeStr == "response.refusal.done"
		if full {
			text = rootResult.Get("refusal").String()
		}
		output += codexClaudeRefusalText(params, codexClaudeRefusalKey(rootResult), text, full)
	} else if typeStr == "response.output_text.done" {
		output += codexClaudeVisibleText(params, codexClaudeRefusalKey(rootResult), rootResult.Get("text").String(), true, false)
	} else if typeStr == "response.output_text.delta" {
		output += codexClaudeVisibleText(params, codexClaudeRefusalKey(rootResult), rootResult.Get("delta").String(), false, false)
	} else if typeStr == "response.content_part.done" {
		if kind := rootResult.Get("part.type").String(); kind == "refusal" || kind == "output_text" {
			field := "part.text"
			if kind == "refusal" {
				field = "part.refusal"
			}
			output += codexClaudeVisibleText(params, codexClaudeRefusalKey(rootResult), rootResult.Get(field).String(), true, kind == "refusal")
		}
		if params.OpenBlockType == codexClaudeTextBlock {
			output += closeCodexClaudeContentBlock(params)
		}
	} else if typeStr == "response.completed" || (typeStr == "response.incomplete" && rootResult.Get("response.incomplete_details.reason").String() == "max_output_tokens") {
		// Recover un-emitted text/refusal suffixes and registered tool arguments
		// from the terminal snapshot, without duplicating prior done events.
		for outputIndex, item := range rootResult.Get("response.output").Array() {
			if item.Get("type").String() == "function_call" {
				event := gjson.Parse(fmt.Sprintf(`{"output_index":%d}`, outputIndex))
				if tool := codexClaudeFindTool(params, codexClaudeFunctionCallKeys(event, item)); tool != nil && !tool.Closed {
					output += codexClaudeToolArguments(params, tool, item.Get("arguments").String(), true)
					tool.Done = true
				}
			}
		}
		for _, tool := range params.Tools {
			tool.Done = true
		}
		output += codexClaudeDrainTools(params)
		for outputIndex, item := range rootResult.Get("response.output").Array() {
			output += codexClaudeMessageSnapshot(params, outputIndex, item)
		}
		output += closeCodexClaudeContentBlock(params)
		template = `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":0,"output_tokens":0}}`
		p := params.HasToolCall
		stopReason := rootResult.Get("response.stop_reason").String()
		if rootResult.Get("response.incomplete_details.reason").String() == "max_output_tokens" {
			template, _ = sjson.Set(template, "delta.stop_reason", "max_tokens")
		} else if p {
			template, _ = sjson.Set(template, "delta.stop_reason", "tool_use")
		} else if stopReason == "max_tokens" || stopReason == "stop" {
			template, _ = sjson.Set(template, "delta.stop_reason", stopReason)
		} else {
			template, _ = sjson.Set(template, "delta.stop_reason", "end_turn")
		}
		inputTokens, outputTokens, cachedTokens := extractResponsesUsage(rootResult.Get("response.usage"))
		template, _ = sjson.Set(template, "usage.input_tokens", inputTokens)
		template, _ = sjson.Set(template, "usage.output_tokens", outputTokens)
		if cachedTokens > 0 {
			template, _ = sjson.Set(template, "usage.cache_read_input_tokens", cachedTokens)
		}

		output += "event: message_delta\n"
		output += fmt.Sprintf("data: %s\n\n", template)
		output += "event: message_stop\n"
		output += `data: {"type":"message_stop"}`
		output += "\n\n"
		params.MessageCompleted = true
	} else if typeStr == "response.output_item.added" {
		itemResult := rootResult.Get("item")
		itemType := itemResult.Get("type").String()
		if itemType == "function_call" {
			name := strings.TrimSpace(itemResult.Get("name").String())
			rev := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)
			if orig, ok := rev[name]; ok {
				name = strings.TrimSpace(orig)
			}
			callID := strings.TrimSpace(itemResult.Get("call_id").String())
			if callID == "" {
				callID = strings.TrimSpace(itemResult.Get("id").String())
			}
			if name == "" || callID == "" {
				return []string{output}
			}

			keys := codexClaudeFunctionCallKeys(rootResult, itemResult)
			if codexClaudeFindTool(params, keys) != nil {
				return []string{output}
			}
			tool := &codexClaudePendingTool{Keys: keys, Name: name, CallID: callID}
			params.Tools = append(params.Tools, tool)
			output += codexClaudeDrainTools(params)
		}
	} else if typeStr == "response.output_item.done" {
		itemResult := rootResult.Get("item")
		itemType := itemResult.Get("type").String()
		if itemType == "message" {
			output += codexClaudeMessageSnapshot(params, int(rootResult.Get("output_index").Int()), itemResult)
		} else if itemType == "function_call" {
			if tool := codexClaudeFindTool(params, codexClaudeFunctionCallKeys(rootResult, itemResult)); tool != nil && !tool.Closed {
				output += codexClaudeToolArguments(params, tool, itemResult.Get("arguments").String(), true)
				tool.Done = true
				output += codexClaudeDrainTools(params)
			}
		}
	} else if typeStr == "response.function_call_arguments.delta" || typeStr == "response.function_call_arguments.done" {
		if tool := codexClaudeFindTool(params, codexClaudeFunctionCallKeys(rootResult, gjson.Result{})); tool != nil && !tool.Closed && !tool.Done {
			full := typeStr == "response.function_call_arguments.done"
			field := "delta"
			if full {
				field = "arguments"
			}
			output += codexClaudeToolArguments(params, tool, rootResult.Get(field).String(), full)
		}
	}

	return []string{output}
}

func codexClaudeRefusalKey(event gjson.Result) string {
	return fmt.Sprintf("%d:%d", event.Get("output_index").Int(), event.Get("content_index").Int())
}

// Refusal is visible assistant text in Claude format. Full done/snapshot events
// may repeat streamed text; only emit a suffix when it extends that same part.
func codexClaudeRefusalText(params *ConvertCodexResponseToClaudeParams, key, text string, full bool) string {
	return codexClaudeVisibleText(params, key, text, full, true)
}

func codexClaudeMessageSnapshot(params *ConvertCodexResponseToClaudeParams, outputIndex int, item gjson.Result) string {
	output := ""
	for contentIndex, part := range item.Get("content").Array() {
		kind := part.Get("type").String()
		if kind != "output_text" && kind != "refusal" {
			continue
		}
		field := "text"
		if kind == "refusal" {
			field = "refusal"
		}
		output += codexClaudeVisibleText(params, fmt.Sprintf("%d:%d", outputIndex, contentIndex), part.Get(field).String(), true, kind == "refusal")
	}
	return output
}

func codexClaudeVisibleText(params *ConvertCodexResponseToClaudeParams, key, text string, full, refusal bool) string {
	if params.RefusalTextByPart == nil {
		params.RefusalTextByPart = make(map[string]string)
	}
	if params.TextByPart == nil {
		params.TextByPart = make(map[string]string)
	}
	parts := params.TextByPart
	if refusal {
		parts = params.RefusalTextByPart
	}
	previous := parts[key]
	if full {
		if !strings.HasPrefix(text, previous) {
			return ""
		}
		text = strings.TrimPrefix(text, previous)
	}
	if text == "" {
		return ""
	}
	parts[key] = previous + text
	output := ""
	if params.OpenBlockType != codexClaudeTextBlock {
		output += closeCodexClaudeContentBlock(params)
		block := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
		block, _ = sjson.Set(block, "index", params.BlockIndex)
		openCodexClaudeContentBlock(params, codexClaudeTextBlock)
		output += "event: content_block_start\n" + fmt.Sprintf("data: %s\n\n", block)
	}
	delta := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`
	delta, _ = sjson.Set(delta, "index", params.OpenBlockIndex)
	delta, _ = sjson.Set(delta, "delta.text", text)
	return output + "event: content_block_delta\n" + fmt.Sprintf("data: %s\n\n", delta)
}

// Keep Claude blocks sequential: parallel upstream tools wait behind the first
// unfinished call, with arguments buffered per identity rather than discarded.
type codexClaudePendingTool struct {
	Keys                  map[string]struct{}
	Name, CallID          string
	Arguments             string
	Emitted               int
	Started, Done, Closed bool
	Index                 int
}

func codexClaudeFindTool(params *ConvertCodexResponseToClaudeParams, keys map[string]struct{}) *codexClaudePendingTool {
	var match *codexClaudePendingTool
	for _, tool := range params.Tools {
		if len(keys) == 0 && (tool.Closed || tool.Done) {
			continue
		}
		compatible, shared := true, false
		for _, prefix := range []string{"output:", "item:", "call:"} {
			a, aok := codexClaudeFunctionCallKeyValue(tool.Keys, prefix)
			b, bok := codexClaudeFunctionCallKeyValue(keys, prefix)
			if aok && bok {
				if a != b {
					compatible = false
				}
				shared = shared || a == b
			}
		}
		if compatible && (shared || len(keys) == 0) {
			if match != nil {
				return nil
			} // Ambiguous identity must not corrupt another call.
			match = tool
		}
	}
	if match != nil {
		for key := range keys {
			match.Keys[key] = struct{}{}
		}
	}
	return match
}

func codexClaudeToolArguments(params *ConvertCodexResponseToClaudeParams, tool *codexClaudePendingTool, args string, full bool) string {
	if full {
		if !strings.HasPrefix(args, tool.Arguments) {
			return ""
		}
		tool.Arguments = args
	} else {
		tool.Arguments += args
	}
	return codexClaudeDrainTools(params)
}

func codexClaudeHasPendingTool(params *ConvertCodexResponseToClaudeParams) bool {
	for _, tool := range params.Tools {
		if !tool.Closed {
			return true
		}
	}
	return false
}

func codexClaudeDrainTools(params *ConvertCodexResponseToClaudeParams) string {
	output := ""
	for _, tool := range params.Tools {
		if tool.Closed {
			continue
		}
		if !tool.Done {
			break
		}
		if !gjson.Valid(tool.Arguments) || !gjson.Parse(tool.Arguments).IsObject() {
			tool.Closed = true // Never expose a truncated/invalid tool as executable.
			continue
		}
		if !tool.Started {
			output += closeCodexClaudeContentBlock(params)
			tool.Started = true
			params.HasToolCall = true
			tool.Index = params.BlockIndex
			block := `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"","name":"","input":{}}}`
			block, _ = sjson.Set(block, "index", tool.Index)
			block, _ = sjson.Set(block, "content_block.id", tool.CallID)
			block, _ = sjson.Set(block, "content_block.name", tool.Name)
			openCodexClaudeContentBlock(params, codexClaudeToolBlock)
			output += "event: content_block_start\n" + fmt.Sprintf("data: %s\n\n", block)
			output += codexClaudeFunctionArgumentsDelta(tool.Index, "")
		}
		if tool.Emitted < len(tool.Arguments) {
			output += codexClaudeFunctionArgumentsDelta(tool.Index, tool.Arguments[tool.Emitted:])
			tool.Emitted = len(tool.Arguments)
		}
		output += closeCodexClaudeContentBlock(params)
		tool.Closed = true
	}
	if !codexClaudeHasPendingTool(params) && len(params.DeferredEvents) > 0 {
		events := params.DeferredEvents
		params.DeferredEvents = nil
		params.ReplayingDeferred = true
		var state any = params
		for _, event := range events {
			output += strings.Join(ConvertCodexResponseToClaude(context.Background(), "", nil, nil, event, &state), "")
		}
		params.ReplayingDeferred = false
	}
	return output
}

func openCodexClaudeContentBlock(params *ConvertCodexResponseToClaudeParams, blockType string) {
	params.OpenBlockType = blockType
	params.OpenBlockIndex = params.BlockIndex
}

func closeCodexClaudeContentBlock(params *ConvertCodexResponseToClaudeParams) string {
	if params.OpenBlockType == "" {
		return ""
	}
	template := `{"type":"content_block_stop","index":0}`
	template, _ = sjson.Set(template, "index", params.OpenBlockIndex)
	if params.BlockIndex <= params.OpenBlockIndex {
		params.BlockIndex = params.OpenBlockIndex + 1
	}
	params.OpenBlockType = ""
	params.OpenBlockIndex = 0
	return "event: content_block_stop\n" + fmt.Sprintf("data: %s\n\n", template)
}

func codexClaudeFunctionArgumentsDelta(blockIndex int, arguments string) string {
	template := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`
	template, _ = sjson.Set(template, "index", blockIndex)
	template, _ = sjson.Set(template, "delta.partial_json", arguments)
	return "event: content_block_delta\n" + fmt.Sprintf("data: %s\n\n", template)
}

func codexClaudeFunctionCallKeys(rootResult, itemResult gjson.Result) map[string]struct{} {
	keys := make(map[string]struct{}, 4)
	if outputIndex := strings.TrimSpace(rootResult.Get("output_index").String()); outputIndex != "" {
		keys["output:"+outputIndex] = struct{}{}
	}
	if itemID := strings.TrimSpace(rootResult.Get("item_id").String()); itemID != "" {
		keys["item:"+itemID] = struct{}{}
	}
	if itemID := strings.TrimSpace(itemResult.Get("id").String()); itemID != "" {
		keys["item:"+itemID] = struct{}{}
	}
	if callID := strings.TrimSpace(rootResult.Get("call_id").String()); callID != "" {
		keys["call:"+callID] = struct{}{}
	}
	if callID := strings.TrimSpace(itemResult.Get("call_id").String()); callID != "" {
		keys["call:"+callID] = struct{}{}
	}
	return keys
}

func codexClaudeFunctionCallKeyValue(keys map[string]struct{}, prefix string) (string, bool) {
	for key := range keys {
		if strings.HasPrefix(key, prefix) {
			return key, true
		}
	}
	return "", false
}

// ConvertCodexResponseToClaudeNonStream converts a non-streaming Codex response to a non-streaming Claude Code response.
// This function processes the complete Codex response and transforms it into a single Claude Code-compatible
// JSON response. It handles message content, tool calls, reasoning content, and usage metadata, combining all
// the information into a single response that matches the Claude Code API format.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response (unused in current implementation)
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for the conversion (unused in current implementation)
//
// Returns:
//   - string: A Claude Code-compatible JSON response containing all message content and metadata
func ConvertCodexResponseToClaudeNonStream(_ context.Context, _ string, originalRequestRawJSON, _ []byte, rawJSON []byte, _ *any) string {
	revNames := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)

	rootResult := gjson.ParseBytes(rawJSON)
	if eventType := rootResult.Get("type").String(); eventType != "response.completed" &&
		!(eventType == "response.incomplete" && rootResult.Get("response.incomplete_details.reason").String() == "max_output_tokens") {
		return ""
	}

	responseData := rootResult.Get("response")
	if !responseData.Exists() {
		return ""
	}

	out := `{"id":"","type":"message","role":"assistant","model":"","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`
	out, _ = sjson.Set(out, "id", responseData.Get("id").String())
	out, _ = sjson.Set(out, "model", responseData.Get("model").String())
	inputTokens, outputTokens, cachedTokens := extractResponsesUsage(responseData.Get("usage"))
	out, _ = sjson.Set(out, "usage.input_tokens", inputTokens)
	out, _ = sjson.Set(out, "usage.output_tokens", outputTokens)
	if cachedTokens > 0 {
		out, _ = sjson.Set(out, "usage.cache_read_input_tokens", cachedTokens)
	}

	hasToolCall := false

	if output := responseData.Get("output"); output.Exists() && output.IsArray() {
		output.ForEach(func(_, item gjson.Result) bool {
			switch item.Get("type").String() {
			case "reasoning":
				thinkingBuilder := strings.Builder{}
				if summary := item.Get("summary"); summary.Exists() {
					if summary.IsArray() {
						summary.ForEach(func(_, part gjson.Result) bool {
							if txt := part.Get("text"); txt.Exists() {
								thinkingBuilder.WriteString(txt.String())
							} else {
								thinkingBuilder.WriteString(part.String())
							}
							return true
						})
					} else {
						thinkingBuilder.WriteString(summary.String())
					}
				}
				if thinkingBuilder.Len() == 0 {
					if content := item.Get("content"); content.Exists() {
						if content.IsArray() {
							content.ForEach(func(_, part gjson.Result) bool {
								if txt := part.Get("text"); txt.Exists() {
									thinkingBuilder.WriteString(txt.String())
								} else {
									thinkingBuilder.WriteString(part.String())
								}
								return true
							})
						} else {
							thinkingBuilder.WriteString(content.String())
						}
					}
				}
				if thinkingBuilder.Len() > 0 {
					block := `{"type":"thinking","thinking":""}`
					block, _ = sjson.Set(block, "thinking", thinkingBuilder.String())
					out, _ = sjson.SetRaw(out, "content.-1", block)
				}
			case "message":
				if content := item.Get("content"); content.Exists() {
					if content.IsArray() {
						content.ForEach(func(_, part gjson.Result) bool {
							if partType := part.Get("type").String(); partType == "output_text" || partType == "refusal" {
								text := part.Get("text").String()
								if partType == "refusal" {
									text = part.Get("refusal").String()
								}
								if text != "" {
									block := `{"type":"text","text":""}`
									block, _ = sjson.Set(block, "text", text)
									out, _ = sjson.SetRaw(out, "content.-1", block)
								}
							}
							return true
						})
					} else {
						text := content.String()
						if text != "" {
							block := `{"type":"text","text":""}`
							block, _ = sjson.Set(block, "text", text)
							out, _ = sjson.SetRaw(out, "content.-1", block)
						}
					}
				}
			case "function_call":
				if responseData.Get("incomplete_details.reason").String() == "max_output_tokens" && !gjson.Valid(item.Get("arguments").String()) {
					// Do not turn a truncated JSON argument into an executable empty tool.
					return true
				}
				name := strings.TrimSpace(item.Get("name").String())
				if original, ok := revNames[name]; ok {
					name = strings.TrimSpace(original)
				}
				callID := strings.TrimSpace(item.Get("call_id").String())
				if callID == "" {
					callID = strings.TrimSpace(item.Get("id").String())
				}
				if name == "" || callID == "" {
					return true
				}
				hasToolCall = true

				toolBlock := `{"type":"tool_use","id":"","name":"","input":{}}`
				toolBlock, _ = sjson.Set(toolBlock, "id", callID)
				toolBlock, _ = sjson.Set(toolBlock, "name", name)
				inputRaw := "{}"
				if argsStr := item.Get("arguments").String(); argsStr != "" && gjson.Valid(argsStr) {
					argsJSON := gjson.Parse(argsStr)
					if argsJSON.IsObject() {
						inputRaw = argsJSON.Raw
					}
				}
				toolBlock, _ = sjson.SetRaw(toolBlock, "input", inputRaw)
				out, _ = sjson.SetRaw(out, "content.-1", toolBlock)
			}
			return true
		})
	}

	if responseData.Get("incomplete_details.reason").String() == "max_output_tokens" {
		out, _ = sjson.Set(out, "stop_reason", "max_tokens")
	} else if stopReason := responseData.Get("stop_reason"); stopReason.Exists() && stopReason.String() != "" {
		if stopReason.String() == "tool_use" && !hasToolCall {
			out, _ = sjson.Set(out, "stop_reason", "end_turn")
		} else {
			out, _ = sjson.Set(out, "stop_reason", stopReason.String())
		}
	} else if hasToolCall {
		out, _ = sjson.Set(out, "stop_reason", "tool_use")
	} else {
		out, _ = sjson.Set(out, "stop_reason", "end_turn")
	}

	if stopSequence := responseData.Get("stop_sequence"); stopSequence.Exists() && stopSequence.String() != "" {
		out, _ = sjson.SetRaw(out, "stop_sequence", stopSequence.Raw)
	}

	return out
}

func extractResponsesUsage(usage gjson.Result) (int64, int64, int64) {
	if !usage.Exists() || usage.Type == gjson.Null {
		return 0, 0, 0
	}

	inputTokens := usage.Get("input_tokens").Int()
	outputTokens := usage.Get("output_tokens").Int()
	cachedTokens := usage.Get("input_tokens_details.cached_tokens").Int()

	if cachedTokens > 0 {
		if inputTokens >= cachedTokens {
			inputTokens -= cachedTokens
		} else {
			inputTokens = 0
		}
	}

	return inputTokens, outputTokens, cachedTokens
}

// buildReverseMapFromClaudeOriginalShortToOriginal builds a map[short]original from original Claude request tools.
func buildReverseMapFromClaudeOriginalShortToOriginal(original []byte) map[string]string {
	tools := gjson.GetBytes(original, "tools")
	rev := map[string]string{}
	if !tools.IsArray() {
		return rev
	}
	var names []string
	arr := tools.Array()
	for i := 0; i < len(arr); i++ {
		n := arr[i].Get("name").String()
		if n != "" {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		m := buildShortNameMap(names)
		for orig, short := range m {
			rev[short] = orig
		}
	}
	return rev
}

func ClaudeTokenCount(ctx context.Context, count int64) string {
	return fmt.Sprintf(`{"input_tokens":%d}`, count)
}
