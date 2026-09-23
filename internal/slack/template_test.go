package slack

import (
	"strings"
	"testing"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

func TestParseTemplateRenderer_MissingBlocks(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"missing both", `{{define "other"}}x{{end}}`},
		{"missing attachments", `{{define "text"}}hi{{end}}`},
		{"missing text", `{{define "attachments"}}[]{{end}}`},
		{"invalid syntax", `{{define "text"}}{{.Oops`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseTemplateRenderer(tt.src); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

const simpleTemplate = `
{{define "text"}}{{.Summary}}{{end}}
{{define "attachments"}}[{{range $i, $item := .Items}}{{if $i}},{{end}}{"color":"#000000","blocks":[{"type":"section","fields":[{"type":"mrkdwn","text":"{{$item.AppName}}"}]}]}{{end}}]{{end}}
`

func TestTemplateRenderer_Render(t *testing.T) {
	r, err := ParseTemplateRenderer(simpleTemplate)
	if err != nil {
		t.Fatalf("ParseTemplateRenderer() error = %v", err)
	}

	n := notification.Notification{
		Summary: "camel — 2 deployed",
		Items: []notification.Item{
			{AppName: "camel-dev-us-east-1"},
			{AppName: "camel-dev-eu-central-1"},
		},
	}
	text, attachments, err := r.Render(n)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if text != n.Summary {
		t.Fatalf("text = %q, want %q", text, n.Summary)
	}
	if !strings.Contains(string(attachments), "camel-dev-us-east-1") || !strings.Contains(string(attachments), "camel-dev-eu-central-1") {
		t.Fatalf("attachments = %s, want both app names", attachments)
	}
}

func TestTemplateRenderer_Render_EmptyAttachmentsOmitted(t *testing.T) {
	r, err := ParseTemplateRenderer(`{{define "text"}}{{.Summary}}{{end}}{{define "attachments"}}   {{end}}`)
	if err != nil {
		t.Fatalf("ParseTemplateRenderer() error = %v", err)
	}
	_, attachments, err := r.Render(notification.Notification{Summary: "s"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if attachments != nil {
		t.Fatalf("attachments = %s, want nil", attachments)
	}
}

func TestTemplateRenderer_Render_InvalidJSON(t *testing.T) {
	r, err := ParseTemplateRenderer(`{{define "text"}}hi{{end}}{{define "attachments"}}{not json{{end}}`)
	if err != nil {
		t.Fatalf("ParseTemplateRenderer() error = %v", err)
	}
	if _, _, err := r.Render(notification.Notification{Summary: "s"}); err == nil {
		t.Fatal("expected error for invalid JSON output, got nil")
	}
}

func TestTemplateRenderer_Render_ExecutionErrorOnMissingField(t *testing.T) {
	r, err := ParseTemplateRenderer(`{{define "text"}}{{.NoSuchField}}{{end}}{{define "attachments"}}[]{{end}}`)
	if err != nil {
		t.Fatalf("ParseTemplateRenderer() error = %v", err)
	}
	if _, _, err := r.Render(notification.Notification{}); err == nil {
		t.Fatal("expected execution error for unknown field, got nil")
	}
}

// TestTemplateRenderer_CompactModeAboveThreshold is a worked example proving
// the motivating problem — a message with many apps looks unwieldy with one
// attachment per app — is solvable purely from a user-supplied template: it
// collapses to a single compact summary attachment once len(.Items)
// crosses a threshold it picks itself, something DefaultRenderer can't do.
const compactTemplate = `
{{define "text"}}{{.Summary}}{{end}}
{{define "attachments"}}
{{- if gt (len .Items) 5 -}}
[{"color":"#2eb1f2","blocks":[{"type":"section","text":{"type":"mrkdwn","text":"{{len .Items}} apps: {{range $i, $item := .Items}}{{if $i}}, {{end}}{{$item.AppName}}{{end}}"}}]}]
{{- else -}}
[{{range $i, $item := .Items}}{{if $i}},{{end}}{"color":"#18be52","blocks":[{"type":"section","fields":[{"type":"mrkdwn","text":"{{$item.AppName}}"}]}]}{{end}}]
{{- end -}}
{{end}}
`

func TestTemplateRenderer_CompactModeAboveThreshold(t *testing.T) {
	r, err := ParseTemplateRenderer(compactTemplate)
	if err != nil {
		t.Fatalf("ParseTemplateRenderer() error = %v", err)
	}

	items := make([]notification.Item, 0, 20)
	for i := range 20 {
		items = append(items, notification.Item{AppName: "app-" + string(rune('a'+i))})
	}

	_, attachments, err := r.Render(notification.Notification{Summary: "s", Items: items})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Count(string(attachments), `"color"`) != 1 {
		t.Fatalf("expected exactly 1 collapsed attachment for 20 items, got: %s", attachments)
	}

	_, attachments, err = r.Render(notification.Notification{Summary: "s", Items: items[:3]})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Count(string(attachments), `"color"`) != 3 {
		t.Fatalf("expected 3 uncollapsed attachments for 3 items, got: %s", attachments)
	}
}
