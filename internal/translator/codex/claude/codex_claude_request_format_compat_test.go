package claude

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertClaudeRequestToCodex_StringTopLevelSystemPreservesRawTextAndOrder(t *testing.T) {
	input := []byte(`{"system":"first\nsecond \"quoted\"","messages":[{"role":"user","content":"after system"}]}`)
	out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)

	items := gjson.GetBytes(out, "input").Array()
	if len(items) != 2 {
		t.Fatalf("expected system instruction before one message, got %s", out)
	}
	if got := items[0].Get("role").String(); got != "developer" {
		t.Errorf("system role = %q, want developer", got)
	}
	if got := items[0].Get("content.0.type").String(); got != "input_text" {
		t.Errorf("system content type = %q, want input_text", got)
	}
	if got, want := items[0].Get("content.0.text").Raw, gjson.GetBytes(input, "system").Raw; got != want {
		t.Errorf("system JSON string changed: got %s, want %s", got, want)
	}
	if got := items[1].Get("content.0.text").String(); got != "after system" {
		t.Errorf("following message moved or changed: %q", got)
	}
}

func TestConvertClaudeRequestToCodex_URLImageSourceUsesImageURL(t *testing.T) {
	input := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/image.png?x=1&y=2"}}]}]}`)
	out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)

	image := gjson.GetBytes(out, "input.0.content.0")
	if got := image.Get("type").String(); got != "input_image" {
		t.Errorf("image type = %q, want input_image", got)
	}
	if got, want := image.Get("image_url").String(), "https://example.com/image.png?x=1&y=2"; got != want {
		t.Errorf("image_url = %q, want %q", got, want)
	}
}

func TestConvertClaudeRequestToCodex_ArraySystemAndBase64ImageRemainUnchanged(t *testing.T) {
	input := []byte(`{"system":[{"type":"text","text":"array instruction"}],"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YWJj"}}]}]}`)
	out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, true)

	items := gjson.GetBytes(out, "input").Array()
	if len(items) != 2 {
		t.Fatalf("expected array system and user message, got %s", out)
	}
	if got := items[0].Get("content.0.text").String(); got != "array instruction" {
		t.Errorf("array system text = %q, want array instruction", got)
	}
	if got, want := items[1].Get("content.0.image_url").String(), "data:image/png;base64,YWJj"; got != want {
		t.Errorf("base64 image_url = %q, want %q", got, want)
	}
}
