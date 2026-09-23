package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/Masterminds/sprig/v3"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

// TemplateRenderer is a user-supplied Renderer: a Go text/template (with
// Sprig functions, matching ArgoCD's own notification templates) containing
// two named blocks, executed against a notification.Notification:
//
//	{{define "text"}}...{{end}}         -> the message's top-level Slack text
//	{{define "attachments"}}...{{end}}  -> raw JSON for the "attachments" (or
//	                                        "blocks") array
//
// Having the whole Notification available — not just one Item at a time —
// is what lets a template collapse to a compact summary once len(.Items)
// crosses whatever threshold its author picks; the built-in DefaultRenderer
// can't do that; it always renders one attachment per app.
type TemplateRenderer struct {
	tmpl *template.Template
}

var _ Renderer = (*TemplateRenderer)(nil)

// ParseTemplateRenderer parses src and validates both required named blocks
// are present. It does not execute the template — a template that parses
// fine can still fail (or produce invalid JSON) against real data, which is
// Render's concern, not this one's.
func ParseTemplateRenderer(src string) (*TemplateRenderer, error) {
	tmpl, err := template.New("message").Funcs(sprig.TxtFuncMap()).Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	if tmpl.Lookup("text") == nil {
		return nil, fmt.Errorf(`template missing required {{define "text"}}...{{end}} block`)
	}
	if tmpl.Lookup("attachments") == nil {
		return nil, fmt.Errorf(`template missing required {{define "attachments"}}...{{end}} block`)
	}
	return &TemplateRenderer{tmpl: tmpl}, nil
}

func (r *TemplateRenderer) Render(n notification.Notification) (string, json.RawMessage, error) {
	var textBuf bytes.Buffer
	if err := r.tmpl.ExecuteTemplate(&textBuf, "text", n); err != nil {
		return "", nil, fmt.Errorf("execute text template: %w", err)
	}

	var attachBuf bytes.Buffer
	if err := r.tmpl.ExecuteTemplate(&attachBuf, "attachments", n); err != nil {
		return "", nil, fmt.Errorf("execute attachments template: %w", err)
	}

	trimmed := bytes.TrimSpace(attachBuf.Bytes())
	if len(trimmed) == 0 {
		return textBuf.String(), nil, nil
	}
	if !json.Valid(trimmed) {
		return "", nil, fmt.Errorf("attachments template did not produce valid JSON: %s", trimmed)
	}
	return textBuf.String(), json.RawMessage(trimmed), nil
}
