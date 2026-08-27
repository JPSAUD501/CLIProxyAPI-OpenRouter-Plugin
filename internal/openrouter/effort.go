package openrouter

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var suffixEffortOrder = map[string]int{
	"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6,
}

func splitEffortSuffix(model string) (string, string, bool) {
	model = strings.TrimSpace(model)
	open := strings.LastIndex(model, "(")
	if open <= 0 || !strings.HasSuffix(model, ")") {
		return model, "", false
	}
	effort := strings.ToLower(strings.TrimSpace(model[open+1 : len(model)-1]))
	if _, supported := suffixEffortOrder[effort]; !supported {
		return model, "", false
	}
	base := strings.TrimSpace(model[:open])
	if base == "" {
		return model, "", false
	}
	return base, effort, true
}

func modelSupportsEffort(models []pluginapi.ModelInfo, modelID, effort string) bool {
	for _, model := range models {
		if !strings.EqualFold(strings.TrimSpace(model.ID), strings.TrimSpace(modelID)) || model.Thinking == nil {
			continue
		}
		return containsFold(model.Thinking.Levels, effort)
	}
	return false
}
