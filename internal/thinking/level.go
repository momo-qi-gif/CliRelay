package thinking

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// ExtractThinkingLevel returns the client's explicit thinking level or numeric
// budget for usage logging, not the final upstream level after normalization or
// payload overrides. Claude output_config.effort is recognized here without
// changing ApplyThinking's shared request parser.
func ExtractThinkingLevel(body []byte, format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "claude" && gjson.ValidBytes(body) {
		if gjson.GetBytes(body, "thinking.type").String() == "disabled" {
			return string(LevelNone)
		}
		if effort := gjson.GetBytes(body, "output_config.effort"); effort.Type == gjson.String {
			switch effort.String() {
			case "low", "medium", "high", "xhigh", "max":
				return effort.String()
			}
		}
	}
	config := extractThinkingConfig(body, format)
	if !hasThinkingConfig(config) {
		return ""
	}

	switch config.Mode {
	case ModeNone:
		return string(LevelNone)
	case ModeAuto:
		return string(LevelAuto)
	case ModeLevel:
		return strings.TrimSpace(string(config.Level))
	case ModeBudget:
		return strconv.Itoa(config.Budget)
	default:
		return ""
	}
}
