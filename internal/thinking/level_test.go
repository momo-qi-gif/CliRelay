package thinking

import "testing"

// Logging must recognize modern Claude effort without changing ApplyThinking's
// shared request parser or inventing a target-model default for an absent effort.
func TestExtractThinkingLevelClaudeOutputEffort(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"adaptive low", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, "low"},
		{"adaptive medium", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"}}`, "medium"},
		{"adaptive high", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, "high"},
		{"adaptive xhigh", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"}}`, "xhigh"},
		{"adaptive max", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`, "max"},
		{"effort without thinking", `{"output_config":{"effort":"high"}}`, "high"},
		{"effort over budget", `{"thinking":{"type":"enabled","budget_tokens":8192},"output_config":{"effort":"low"}}`, "low"},
		{"disabled priority", `{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`, "none"},
		{"adaptive missing", `{"thinking":{"type":"adaptive"}}`, ""},
		{"adaptive unknown", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"ultra"}}`, ""},
		{"array rejected", `{"output_config":{"effort":["high"]}}`, ""},
		{"object rejected", `{"output_config":{"effort":{"level":"high"}}}`, ""},
		{"null rejected", `{"output_config":{"effort":null}}`, ""},
		{"boolean rejected", `{"output_config":{"effort":true}}`, ""},
		{"number rejected", `{"output_config":{"effort":8192}}`, ""},
		{"empty rejected", `{"output_config":{"effort":""}}`, ""},
		{"whitespace rejected", `{"output_config":{"effort":" high "}}`, ""},
		{"case rejected", `{"output_config":{"effort":"HIGH"}}`, ""},
		{"legacy budget", `{"thinking":{"type":"enabled","budget_tokens":8192}}`, "8192"},
		{"invalid preserves budget", `{"thinking":{"type":"enabled","budget_tokens":8192},"output_config":{"effort":"ultra"}}`, "8192"},
		{"legacy enabled", `{"thinking":{"type":"enabled"}}`, "auto"},
		{"absent", `{}`, ""},
		{"malformed", `{"output_config":{"effort":"high"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractThinkingLevel([]byte(tc.body), " Claude "); got != tc.want {
				t.Fatalf("log thinking level = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractThinkingLevelLeavesOtherProtocolsUnchanged(t *testing.T) {
	for _, tc := range []struct{ format, body, want string }{
		{"codex", `{"reasoning":{"effort":"high"},"output_config":{"effort":"low"}}`, "high"},
		{"openai", `{"reasoning_effort":"medium","output_config":{"effort":"high"}}`, "medium"},
		{"unknown", `{"output_config":{"effort":"high"}}`, ""},
	} {
		if got := ExtractThinkingLevel([]byte(tc.body), tc.format); got != tc.want {
			t.Errorf("%s level = %q, want %q", tc.format, got, tc.want)
		}
	}
}
