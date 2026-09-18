package render

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

func TestBuildMessage_NoEvents(t *testing.T) {
	_, err := BuildMessage(nil)
	if !errors.Is(err, ErrNoEvents) {
		t.Fatalf("BuildMessage(nil) error = %v, want ErrNoEvents", err)
	}
}

func TestBuildMessage_AllKnownTriggers(t *testing.T) {
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
			ev := event.Event{
				GroupKey:  "camel",
				AppName:   "camel-dev-eu-central-1",
				Trigger:   tt.trigger,
				Revision:  "abc12345",
				RepoURL:   "https://github.com/timescale/savannah-camel.git",
				Target:    "dev-eu-central-1",
				ArgoCDURL: "https://argocd.example.com",
			}
			msg, err := BuildMessage(map[string]event.Event{ev.AppName: ev})
			if err != nil {
				t.Fatalf("BuildMessage() error = %v", err)
			}
			if len(msg.Attachments) != 1 {
				t.Fatalf("expected 1 attachment, got %d", len(msg.Attachments))
			}
			if msg.Attachments[0].Color != tt.wantColor {
				t.Fatalf("color = %q, want %q", msg.Attachments[0].Color, tt.wantColor)
			}
		})
	}
}

func TestBuildMessage_DeletedHasNoArgoCDButton(t *testing.T) {
	ev := event.Event{GroupKey: "camel", AppName: "camel-dev", Trigger: "on-deleted"}
	msg, err := BuildMessage(map[string]event.Event{ev.AppName: ev})
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	for _, block := range msg.Attachments[0].Blocks {
		if block["type"] == "actions" {
			t.Fatalf("expected no actions block for on-deleted, got %v", block)
		}
	}
}

func TestBuildMessage_SyncFailedTruncatesMessage(t *testing.T) {
	ev := event.Event{
		GroupKey:     "camel",
		AppName:      "camel-dev",
		Trigger:      "on-sync-failed",
		SyncPhase:    "Failed",
		OperationMsg: strings.Repeat("x", 400),
	}
	msg, err := BuildMessage(map[string]event.Event{ev.AppName: ev})
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	section := msg.Attachments[0].Blocks[1]
	text := section["text"].(map[string]any)["text"].(string)
	if len(text) != 303 || !strings.HasSuffix(text, "...") {
		t.Fatalf("expected truncated 300-char message with ellipsis, got len=%d", len(text))
	}
}

func TestBuildMessage_UnknownTriggerSkippedButCounted(t *testing.T) {
	known := event.Event{GroupKey: "camel", AppName: "app-a", Trigger: "on-deployed"}
	unknown := event.Event{GroupKey: "camel", AppName: "app-b", Trigger: "on-mystery"}

	msg, err := BuildMessage(map[string]event.Event{known.AppName: known, unknown.AppName: unknown})
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("expected unknown trigger to be skipped from attachments, got %d", len(msg.Attachments))
	}
	if !strings.Contains(msg.Text, "mystery") {
		t.Fatalf("expected summary to still count unknown trigger, got %q", msg.Text)
	}
}

func TestBuildMessage_SortedByAppName(t *testing.T) {
	perApp := map[string]event.Event{
		"zebra": {GroupKey: "camel", AppName: "zebra", Trigger: "on-deployed"},
		"alpha": {GroupKey: "camel", AppName: "alpha", Trigger: "on-deployed"},
		"mid":   {GroupKey: "camel", AppName: "mid", Trigger: "on-deployed"},
	}

	for range 5 {
		msg, err := BuildMessage(perApp)
		if err != nil {
			t.Fatalf("BuildMessage() error = %v", err)
		}
		fields := msg.Attachments[0].Blocks[0]["fields"].([]map[string]any)
		appField := fields[0]["text"].(string)
		if !strings.Contains(appField, "alpha") {
			t.Fatalf("expected first attachment to be for 'alpha', got %q", appField)
		}
	}
}

func TestBuildMessage_OverflowChunking(t *testing.T) {
	perApp := make(map[string]event.Event, 55)
	for i := range 55 {
		name := "app-" + strconv.Itoa(i)
		perApp[name] = event.Event{GroupKey: "camel", AppName: name, Trigger: "on-deployed"}
	}

	msg, err := BuildMessage(perApp)
	if err != nil {
		t.Fatalf("BuildMessage() error = %v", err)
	}
	if len(msg.Attachments) != maxAttachments {
		t.Fatalf("expected %d attachments after chunking, got %d", maxAttachments, len(msg.Attachments))
	}
	last := msg.Attachments[maxAttachments-1]
	text := last.Blocks[0]["text"].(map[string]any)["text"].(string)
	if !strings.Contains(text, fmt.Sprintf("+%d more", 55-(maxAttachments-1))) {
		t.Fatalf("expected overflow marker, got %q", text)
	}
}

func TestCommitLink(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		revision string
		want     string
	}{
		{name: "trims .git and shortens revision", repoURL: "https://github.com/timescale/savannah-camel.git", revision: "abcdef1234567", want: "<https://github.com/timescale/savannah-camel/commit/abcdef1234567|`abcdef12`>"},
		{name: "empty repo falls back to revision", repoURL: "", revision: "abcdef1234567", want: "abcdef1234567"},
		{name: "empty revision falls back to ?", repoURL: "https://example.com/repo.git", revision: "", want: "?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commitLink(tt.repoURL, tt.revision); got != tt.want {
				t.Fatalf("commitLink() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate() = %q, want unchanged", got)
	}
	if got := truncate("this is long", 4); got != "this..." {
		t.Fatalf("truncate() = %q, want truncated with ellipsis", got)
	}
}
