package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/owlcms/replays/internal/recording"
)

type detectionProgressTemplate struct {
	Stage         string `json:"stage"`
	Detail        string `json:"detail,omitempty"`
	StatusKey     string `json:"statusKey,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	ReplaceStatus bool   `json:"replaceStatus,omitempty"`
	HasError      bool   `json:"hasError,omitempty"`
}

type detectionProgressUI struct {
	InitialStage                string `json:"initialStage"`
	CloseButtonLabel            string `json:"closeButtonLabel"`
	CurrentStageLabel           string `json:"currentStageLabel"`
	CurrentActivityLabel        string `json:"currentActivityLabel"`
	SourceStatusLabel           string `json:"sourceStatusLabel"`
	FailureStatusLabel          string `json:"failureStatusLabel"`
	DetectingSourcesTitle       string `json:"detectingSourcesTitle"`
	RescanningSourcesTitle      string `json:"rescanningSourcesTitle"`
	DetectingSourcesStatus      string `json:"detectingSourcesStatus"`
	RescanButtonLabel           string `json:"rescanButtonLabel"`
	RescanningSourcesStatus     string `json:"rescanningSourcesStatus"`
	RescanFailed                string `json:"rescanFailed"`
	RescanCompletedWithErrors   string `json:"rescanCompletedWithErrors"`
	RescanCompleted             string `json:"rescanCompleted"`
	DetectionCompletedWithErrors string `json:"detectionCompletedWithErrors"`
}

//go:embed detection_progress_texts.json
var detectionProgressTemplatesJSON []byte

//go:embed detection_progress_ui.json
var detectionProgressUIJSON []byte

var detectionProgressTemplates = mustParseDetectionProgressTemplates()
var detectionProgressUIStrings = mustParseDetectionProgressUI()

func mustParseDetectionProgressTemplates() map[string]detectionProgressTemplate {
	var templates map[string]detectionProgressTemplate
	if err := json.Unmarshal(detectionProgressTemplatesJSON, &templates); err != nil {
		panic(fmt.Sprintf("invalid detection progress templates: %v", err))
	}
	return templates
}

func mustParseDetectionProgressUI() detectionProgressUI {
	var ui detectionProgressUI
	if err := json.Unmarshal(detectionProgressUIJSON, &ui); err != nil {
		panic(fmt.Sprintf("invalid detection progress UI strings: %v", err))
	}
	return ui
}

func renderDetectionProgressText(templateText, payload string) string {
	name, detail := recording.ProgressPayloadParts(payload)
	if detail == "" {
		templateText = strings.ReplaceAll(templateText, ": {detail}", "")
	}
	rendered := strings.ReplaceAll(templateText, "{payload}", payload)
	rendered = strings.ReplaceAll(rendered, "{name}", name)
	rendered = strings.ReplaceAll(rendered, "{detail}", detail)
	return rendered
}

func renderDetectionProgressCopy(templateText string, values map[string]string) string {
	rendered := templateText
	for key, value := range values {
		rendered = strings.ReplaceAll(rendered, "{"+key+"}", value)
	}
	return rendered
}

func detectionProgressUpdateForTag(tag, payload string) (detectionProgressUpdate, bool) {
	template, ok := detectionProgressTemplates[tag]
	if !ok {
		return detectionProgressUpdate{}, false
	}

	return detectionProgressUpdate{
		stage:          renderDetectionProgressText(template.Stage, payload),
		detail:         renderDetectionProgressText(template.Detail, payload),
		statusKey:      renderDetectionProgressText(template.StatusKey, payload),
		statusMessage:  renderDetectionProgressText(template.StatusMessage, payload),
		replaceStatus:  template.ReplaceStatus,
		hasError:       template.HasError,
		statusHasError: template.HasError,
	}, true
}
