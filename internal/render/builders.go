package render

import (
	"fmt"
	"strings"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

type attachmentBuilder func(ev event.Event) Attachment

var triggerBuilders = map[string]attachmentBuilder{
	"on-created":             buildCreatedAttachment,
	"on-deleted":             buildDeletedAttachment,
	"on-deployed":            buildDeployedAttachment,
	"on-health-degraded":     buildHealthDegradedAttachment,
	"on-sync-failed":         buildSyncFailedAttachment,
	"on-sync-running":        buildSyncRunningAttachment,
	"on-sync-status-unknown": buildSyncStatusUnknownAttachment,
	"on-sync-succeeded":      buildSyncSucceededAttachment,
}

func buildCreatedAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#2eb1f2",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
				},
			},
			openInArgoCDBlock(ev),
		},
	}
}

// buildDeletedAttachment omits the "Open in ArgoCD" button — the Application
// no longer exists to open.
func buildDeletedAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#8a8a8a",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
				},
			},
		},
	}
}

func buildDeployedAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#18be52",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
					mrkdwnField("Commit", commitLink(ev.RepoURL, ev.Revision)),
					mrkdwnField("Triggered by", orDefault(ev.InitiatedBy, "auto-sync")),
					mrkdwnField("Images", formatImages(ev.Images)),
				},
			},
			openInArgoCDBlock(ev),
		},
	}
}

func buildSyncFailedAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#E96D76",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
					mrkdwnField("Phase", ev.SyncPhase),
				},
			},
			{
				"type": "section",
				"text": mrkdwnText(truncate(ev.OperationMsg, 300)),
			},
			openInArgoCDBlock(ev),
		},
	}
}

func buildHealthDegradedAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#f4c030",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
					mrkdwnField("Health", ":large_yellow_circle: Degraded"),
				},
			},
			openInArgoCDBlock(ev),
		},
	}
}

func buildSyncRunningAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#2496ed",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
					mrkdwnField("Commit", commitLink(ev.RepoURL, ev.Revision)),
					mrkdwnField("Triggered by", orDefault(ev.InitiatedBy, "auto-sync")),
				},
			},
			openInArgoCDBlock(ev),
		},
	}
}

func buildSyncStatusUnknownAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#9b9b9b",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
					mrkdwnField("Sync Status", orDefault(ev.SyncStatus, "Unknown")),
				},
			},
			openInArgoCDBlock(ev),
		},
	}
}

func buildSyncSucceededAttachment(ev event.Event) Attachment {
	return Attachment{
		Color: "#2eb886",
		Blocks: []map[string]any{
			{
				"type": "section",
				"fields": []map[string]any{
					mrkdwnField("App", ev.AppName),
					mrkdwnField("Cluster", orDefault(ev.Target, "?")),
					mrkdwnField("Commit", commitLink(ev.RepoURL, ev.Revision)),
				},
			},
			openInArgoCDBlock(ev),
		},
	}
}

func openInArgoCDBlock(ev event.Event) map[string]any {
	return map[string]any{
		"type": "actions",
		"elements": []map[string]any{
			{
				"type": "button",
				"text": map[string]any{"type": "plain_text", "text": "Open in ArgoCD"},
				"url":  fmt.Sprintf("%s/applications/%s", strings.TrimRight(ev.ArgoCDURL, "/"), ev.AppName),
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

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func formatImages(images []string) string {
	if len(images) == 0 {
		return "none"
	}
	return strings.Join(images, ", ")
}

// commitLink mirrors the existing ArgoCD Slack templates' "<repo/commit/rev|`rev`>"
// hyperlink, trimming the ".git" suffix repoURLs typically carry.
func commitLink(repoURL, revision string) string {
	if repoURL == "" || revision == "" {
		return orDefault(revision, "?")
	}
	short := revision
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("<%s/commit/%s|`%s`>", strings.TrimSuffix(repoURL, ".git"), revision, short)
}
