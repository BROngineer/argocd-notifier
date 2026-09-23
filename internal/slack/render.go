package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

// Renderer turns an aggregated Notification into a Slack message body — text
// is chat.postMessage/chat.update's top-level "text", attachments is a raw
// JSON value (an "attachments" array, or a "blocks" array a custom template
// builds instead) embedded as-is into the request. Injected into Client so a
// user-supplied template (TemplateRenderer) can replace the built-in
// per-trigger rendering without Client itself knowing the difference.
type Renderer interface {
	Render(n notification.Notification) (text string, attachments json.RawMessage, err error)
}

// DefaultRenderer is the built-in, always-available renderer: one colored
// attachment per app Item, via the per-trigger builders below. Used when no
// custom template is configured, and as the safe fallback when one fails.
type DefaultRenderer struct{}

var _ Renderer = DefaultRenderer{}

func (DefaultRenderer) Render(n notification.Notification) (string, json.RawMessage, error) {
	text, attachments := renderNotification(n)
	if len(attachments) == 0 {
		return text, nil, nil
	}
	raw, err := json.Marshal(attachments)
	if err != nil {
		return "", nil, fmt.Errorf("marshal attachments: %w", err)
	}
	return text, raw, nil
}

type attachment struct {
	Color  string           `json:"color"`
	Blocks []map[string]any `json:"blocks"`
}

// maxAttachments matches Slack's documented limit for the (legacy)
// attachments field on chat.postMessage/chat.update — not to be confused
// with the 50-block limit on the newer top-level "blocks" field.
const maxAttachments = 100

type itemRenderer func(item notification.Item) attachment

var triggerRenderers = map[string]itemRenderer{
	"on-created":             renderCreated,
	"on-deleted":             renderDeleted,
	"on-deployed":            renderDeployed,
	"on-health-degraded":     renderHealthDegraded,
	"on-sync-failed":         renderSyncFailed,
	"on-sync-running":        renderSyncRunning,
	"on-sync-status-unknown": renderSyncStatusUnknown,
	"on-sync-succeeded":      renderSyncSucceeded,
}

func renderNotification(n notification.Notification) (string, []attachment) {
	attachments := make([]attachment, 0, len(n.Items))
	for _, item := range n.Items {
		render, ok := triggerRenderers[item.Trigger]
		if !ok {
			continue
		}
		attachments = append(attachments, render(item))
	}

	if len(attachments) > maxAttachments {
		overflow := len(attachments) - (maxAttachments - 1)
		attachments = append(attachments[:maxAttachments-1], moreAttachment(overflow))
	}

	return n.Summary, attachments
}

func renderCreated(item notification.Item) attachment {
	return attachment{
		Color: "#2eb1f2",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", item.AppName),
					mrkdwnField("Cluster", item.Cluster),
				},
			},
			openInArgoCDBlock(item.Link),
		},
	}
}

// renderDeleted omits the "Open in ArgoCD" button — the Application no
// longer exists to open.
func renderDeleted(item notification.Item) attachment {
	return attachment{
		Color: "#8a8a8a",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", item.AppName),
					mrkdwnField("Cluster", item.Cluster),
				},
			},
		},
	}
}

func renderDeployed(item notification.Item) attachment {
	return attachment{
		Color: "#18be52",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", item.AppName),
					mrkdwnField("Cluster", item.Cluster),
					mrkdwnField("Commit", commitText(item)),
					mrkdwnField("Triggered by", item.TriggeredBy),
					mrkdwnField("Images", formatImages(item.Images)),
				},
			},
			openInArgoCDBlock(item.Link),
		},
	}
}

func renderSyncFailed(item notification.Item) attachment {
	return attachment{
		Color: "#E96D76",
		Blocks: []map[string]any{
			{
				"type":   "section",
				"fields": append(baseFields(item), fieldsAsMrkdwn(item.Fields)...),
			},
			{
				"type": "section",
				"text": mrkdwnText(item.DetailText),
			},
			openInArgoCDBlock(item.Link),
		},
	}
}

func renderHealthDegraded(item notification.Item) attachment {
	return attachment{
		Color: "#f4c030",
		Blocks: []map[string]any{
			{
				"type":   "section",
				"fields": append(baseFields(item), fieldsAsMrkdwn(item.Fields)...),
			},
			openInArgoCDBlock(item.Link),
		},
	}
}

func renderSyncRunning(item notification.Item) attachment {
	return attachment{
		Color: "#2496ed",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", item.AppName),
					mrkdwnField("Cluster", item.Cluster),
					mrkdwnField("Commit", commitText(item)),
					mrkdwnField("Triggered by", item.TriggeredBy),
				},
			},
			openInArgoCDBlock(item.Link),
		},
	}
}

func renderSyncStatusUnknown(item notification.Item) attachment {
	return attachment{
		Color: "#9b9b9b",
		Blocks: []map[string]any{
			{
				"type":   "section",
				"fields": append(baseFields(item), fieldsAsMrkdwn(item.Fields)...),
			},
			openInArgoCDBlock(item.Link),
		},
	}
}

func renderSyncSucceeded(item notification.Item) attachment {
	return attachment{
		Color: "#2eb886",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", item.AppName),
					mrkdwnField("Cluster", item.Cluster),
					mrkdwnField("Commit", commitText(item)),
				},
			},
			openInArgoCDBlock(item.Link),
		},
	}
}

func baseFields(item notification.Item) []map[string]any {
	return []map[string]any{
		mrkdwnField("App", item.AppName),
		mrkdwnField("Cluster", item.Cluster),
	}
}

func fieldsAsMrkdwn(fields []notification.Field) []map[string]any {
	out := make([]map[string]any, 0, len(fields))
	for _, f := range fields {
		out = append(out, mrkdwnField(f.Label, f.Value))
	}
	return out
}

func openInArgoCDBlock(link string) map[string]any {
	return map[string]any{
		"type": "actions",
		"elements": []map[string]any{
			{
				"type": "button",
				"text": map[string]any{"type": "plain_text", "text": "Open in ArgoCD"},
				"url":  link,
			},
		},
	}
}

func mrkdwnField(label, value string) map[string]any {
	return mrkdwnText(fmt.Sprintf("*%s*\n%s", label, value))
}

func mrkdwnText(text string) map[string]any {
	return map[string]any{"type": "mrkdwn", "text": text}
}

func formatImages(images []string) string {
	if len(images) == 0 {
		return "none"
	}
	return strings.Join(images, ", ")
}

// commitText renders item.CommitSHA as a hyperlink to item.CommitURL when
// available, or plain text otherwise — this is where Slack's "<url|text>"
// markup syntax lives, kept out of the backend-neutral notification model.
func commitText(item notification.Item) string {
	if item.CommitURL == "" {
		return fmt.Sprintf("`%s`", item.CommitSHA)
	}
	return fmt.Sprintf("<%s|`%s`>", item.CommitURL, item.CommitSHA)
}

func moreAttachment(overflow int) attachment {
	return attachment{
		Color: "#9b9b9b",
		Blocks: []map[string]any{
			{
				"type": "section",
				"text": mrkdwnText(fmt.Sprintf("_+%d more_", overflow)),
			},
		},
	}
}
