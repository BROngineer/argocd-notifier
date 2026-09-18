package slack

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

func TestRenderNotification_AllKnownTriggers(t *testing.T) {
	tests := []struct {
		trigger   string
		wantColor string
	}{
		{"on-created", "#2eb1f2"},
		{"on-deleted", "#8a8a8a"},
		{"on-deployed", "#18be52"},
		{"on-health-degraded", "#f4c030"},
		{"on-sync-failed", "#E96D76"},
		{"on-sync-running", "#2496ed"},
		{"on-sync-status-unknown", "#9b9b9b"},
		{"on-sync-succeeded", "#2eb886"},
	}

	for _, tt := range tests {
		t.Run(tt.trigger, func(t *testing.T) {
			item := notification.Item{AppName: "camel-dev", Cluster: "dev-eu-central-1", Trigger: tt.trigger, Link: "https://argocd.example.com/applications/camel-dev"}
			_, attachments := renderNotification(notification.Notification{Summary: "s", Items: []notification.Item{item}})
			if len(attachments) != 1 {
				t.Fatalf("expected 1 attachment, got %d", len(attachments))
			}
			if attachments[0].Color != tt.wantColor {
				t.Fatalf("color = %q, want %q", attachments[0].Color, tt.wantColor)
			}
		})
	}
}

func TestRenderNotification_DeletedHasNoButton(t *testing.T) {
	item := notification.Item{AppName: "camel-dev", Trigger: "on-deleted"}
	_, attachments := renderNotification(notification.Notification{Summary: "s", Items: []notification.Item{item}})
	for _, block := range attachments[0].Blocks {
		if block["type"] == "actions" {
			t.Fatalf("expected no actions block for on-deleted, got %v", block)
		}
	}
}

func TestRenderNotification_SyncFailedIncludesDetailText(t *testing.T) {
	item := notification.Item{
		AppName:    "camel-dev",
		Trigger:    "on-sync-failed",
		Fields:     []notification.Field{{Label: "Phase", Value: "Failed"}},
		DetailText: strings.Repeat("x", 300) + "...",
	}
	_, attachments := renderNotification(notification.Notification{Summary: "s", Items: []notification.Item{item}})
	text := attachments[0].Blocks[1]["text"].(map[string]any)["text"].(string)
	if !strings.HasSuffix(text, "...") {
		t.Fatalf("expected detail text block, got %q", text)
	}
}

func TestRenderNotification_UnknownTriggerSkipped(t *testing.T) {
	items := []notification.Item{
		{AppName: "app-a", Trigger: "on-deployed"},
		{AppName: "app-b", Trigger: "on-mystery"},
	}
	_, attachments := renderNotification(notification.Notification{Summary: "s", Items: items})
	if len(attachments) != 1 {
		t.Fatalf("expected unknown trigger to be skipped, got %d attachments", len(attachments))
	}
}

func TestRenderNotification_OverflowChunking(t *testing.T) {
	items := make([]notification.Item, 0, 105)
	for i := range 105 {
		items = append(items, notification.Item{AppName: "app-" + strconv.Itoa(i), Trigger: "on-deployed"})
	}
	_, attachments := renderNotification(notification.Notification{Summary: "s", Items: items})
	if len(attachments) != maxAttachments {
		t.Fatalf("expected %d attachments after chunking, got %d", maxAttachments, len(attachments))
	}
	last := attachments[maxAttachments-1]
	text := last.Blocks[0]["text"].(map[string]any)["text"].(string)
	if !strings.Contains(text, fmt.Sprintf("+%d more", 105-(maxAttachments-1))) {
		t.Fatalf("expected overflow marker, got %q", text)
	}
}

func TestCommitText(t *testing.T) {
	tests := []struct {
		name string
		item notification.Item
		want string
	}{
		{
			name: "with url renders hyperlink",
			item: notification.Item{CommitURL: "https://github.com/timescale/savannah-camel/commit/abcdef12", CommitSHA: "abcdef12"},
			want: "<https://github.com/timescale/savannah-camel/commit/abcdef12|`abcdef12`>",
		},
		{
			name: "without url renders plain text",
			item: notification.Item{CommitSHA: "?"},
			want: "`?`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commitText(tt.item); got != tt.want {
				t.Fatalf("commitText() = %q, want %q", got, tt.want)
			}
		})
	}
}
