package claude

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

// These cases catch adaptive thinking overriding the client's explicit effort,
// including non-string values accidentally being accepted as effort levels.
func TestConvertClaudeRequestToCodex_OutputConfigEffort(t *testing.T) {
	cases := []struct {
		name   string
		fields string
		want   string
	}{
		{"adaptive_low", `"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}`, "low"},
		{"adaptive_medium", `"thinking":{"type":"adaptive","display":"updates"},"output_config":{"effort":"medium"}`, "medium"},
		{"adaptive_high", `"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}`, "high"},
		{"adaptive_xhigh", `"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"}`, "xhigh"},
		{"adaptive_max", `"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}`, "max"},
		{"effort_without_thinking", `"output_config":{"effort":"low"}`, "low"},
		{"effort_over_budget", `"thinking":{"type":"enabled","budget_tokens":32768},"output_config":{"effort":"medium"}`, "medium"},
		{"disabled_stays_disabled", `"thinking":{"type":"disabled"},"output_config":{"effort":"high"}`, "none"},
		{"adaptive_missing_effort", `"thinking":{"type":"adaptive"}`, "xhigh"},
		{"adaptive_empty_effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":""}`, "xhigh"},
		{"adaptive_unknown_effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":"ultra"}`, "xhigh"},
		{"adaptive_null_effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":null}`, "xhigh"},
		{"adaptive_numeric_effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":8192}`, "xhigh"},
		{"adaptive_boolean_effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":true}`, "xhigh"},
		{"adaptive_object_effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":{"level":"low"}}`, "xhigh"},
		{"adaptive_array_effort", `"thinking":{"type":"adaptive"},"output_config":{"effort":["low"]}`, "xhigh"},
		{"legacy_budget_low", `"thinking":{"type":"enabled","budget_tokens":1024}`, "low"},
		{"legacy_budget_medium", `"thinking":{"type":"enabled","budget_tokens":8192}`, "medium"},
		{"legacy_budget_high", `"thinking":{"type":"enabled","budget_tokens":16384}`, "high"},
		{"legacy_budget_xhigh", `"thinking":{"type":"enabled","budget_tokens":32768}`, "xhigh"},
		{"invalid_effort_preserves_budget", `"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"ultra"}`, "low"},
		{"legacy_enabled_without_budget", `"thinking":{"type":"enabled"}`, "medium"},
		{"default_without_thinking", `"output_config":{}`, "medium"},
	}
	for _, tc := range cases {
		for _, stream := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				input := []byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"Reply PONG"}],` + tc.fields + `}`)
				out := ConvertClaudeRequestToCodex("gpt-6.1-sol", input, stream)
				if !gjson.ValidBytes(out) {
					t.Fatalf("invalid output JSON: %s", out)
				}
				if got := gjson.GetBytes(out, "reasoning.effort").String(); got != tc.want {
					t.Fatalf("reasoning.effort = %q, want %q", got, tc.want)
				}
			})
		}
	}
}
