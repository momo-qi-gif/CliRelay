package executor

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

func TestExecutionContextReporterClaudeOutputEffort(t *testing.T) {
	for _, tc := range []struct{ name, model, original, want string }{
		{"client medium", "gpt-6.1-sol", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"}}`, "medium"},
		{"client high", "gpt-6.1-sol", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, "high"},
		{"suffix wins", "gpt-6.1-sol(low)", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, "low"},
		{"disabled", "gpt-6.1-sol", `{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`, "none"},
		{"no inferred target default", "gpt-6.1-sol", `{"thinking":{"type":"adaptive"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Deliberately different from OriginalRequest: a translated/overridden
			// payload must not be mislabeled as the client's requested log value.
			ec := newExecutionContext(context.Background(), "codex", &config.Config{}, nil,
				cliproxyexecutor.Request{Model: tc.model, Payload: []byte(`{"reasoning":{"effort":"xhigh"}}`)},
				cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: []byte(tc.original)},
				ExecutionOptions{})
			reporter := ec.Reporter()
			if got := reporter.thinkingLevel; got != tc.want {
				t.Fatalf("reporter log thinking level = %q, want %q", got, tc.want)
			}
		})
	}
}
