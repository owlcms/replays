package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

type detectionProgressTemplate struct {
	Stage         string `json:"stage"`
	Detail        string `json:"detail,omitempty"`
	StatusKey     string `json:"statusKey,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	ReplaceStatus bool   `json:"replaceStatus,omitempty"`
	HasError      bool   `json:"hasError,omitempty"`
}

//go:embed detection_progress_texts.json
var detectionProgressTemplatesJSON []byte

var detectionProgressTemplates = mustParseDetectionProgressTemplates()

func mustParseDetectionProgressTemplates() map[string]detectionProgressTemplate {
	var templates map[string]detectionProgressTemplate
	if err := json.Unmarshal(detectionProgressTemplatesJSON, &templates); err != nil {
		panic(fmt.Sprintf("invalid detection progress templates: %v", err))
	}
	return templates
}

func renderDetectionProgressText(templateText, payload string) string {
	return strings.ReplaceAll(templateText, "{payload}", payload)
}

func detectionProgressUpdateForTag(tag, payload string) (detectionProgressUpdate, bool) {
	template, ok := detectionProgressTemplates[tag]
	if !ok {
		return detectionProgressUpdate{}, false
	}

	return detectionProgressUpdate{
		stage:         renderDetectionProgressText(template.Stage, payload),
		detail:        renderDetectionProgressText(template.Detail, payload),
		statusKey:     renderDetectionProgressText(template.StatusKey, payload),
		statusMessage: renderDetectionProgressText(template.StatusMessage, payload),
		replaceStatus: template.ReplaceStatus,
		hasError:      template.HasError,
	}, true
}
