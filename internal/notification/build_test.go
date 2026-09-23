package notification

import (
	"errors"
	"strings"
	"testing"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

func TestBuild_NoEvents(t *testing.T) {
	_, err := Build(nil)
	if !errors.Is(err, ErrNoEvents) {
		t.Fatalf("Build(nil) error = %v, want ErrNoEvents", err)
	}
}

func baseTestEvent(trigger string) event.Event {
	return event.Event{
		GroupKey:       "tatooine",
		AppName:        "tatooine-dev-empire",
		Trigger:        trigger,
		Revision:       "abcdef1234567",
		RepoURL:        "https://github.com/timescale/savannah-tatooine.git",
		Target:         "dev-empire",
		ArgoCDURL:      "https://argocd.example.com",
		TargetRevision: "v1.0.0",
	}
}

func TestBuild_Deployed(t *testing.T) {
	ev := baseTestEvent("on-deployed")
	ev.InitiatedBy = "alice"
	ev.Images = []string{"repo/img:v1"}

	n, err := Build(map[string]event.Event{ev.AppName: ev})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(n.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(n.Items))
	}
	item := n.Items[0]
	if item.Cluster != "dev-empire" {
		t.Errorf("Cluster = %q", item.Cluster)
	}
	if item.CommitURL != "https://github.com/timescale/savannah-tatooine/commit/abcdef1234567" {
		t.Errorf("CommitURL = %q", item.CommitURL)
	}
	if item.CommitSHA != "abcdef12" {
		t.Errorf("CommitSHA = %q", item.CommitSHA)
	}
	if item.TriggeredBy != "alice" {
		t.Errorf("TriggeredBy = %q", item.TriggeredBy)
	}
	if len(item.Images) != 1 || item.Images[0] != "repo/img:v1" {
		t.Errorf("Images = %v", item.Images)
	}
	if item.Link != "https://argocd.example.com/applications/tatooine-dev-empire" {
		t.Errorf("Link = %q", item.Link)
	}
	if item.TargetRevision != "v1.0.0" {
		t.Errorf("TargetRevision = %q, want v1.0.0", item.TargetRevision)
	}
}

// TestBuild_TargetRevisionPopulatedForEveryTrigger confirms TargetRevision
// isn't curated per-trigger the way CommitSHA/Fields are — every builder
// carries it straight through, since a template author might want it
// regardless of which trigger fired.
func TestBuild_TargetRevisionPopulatedForEveryTrigger(t *testing.T) {
	triggers := []string{
		"on-created", "on-deleted", "on-deployed", "on-health-degraded",
		"on-sync-failed", "on-sync-running", "on-sync-status-unknown", "on-sync-succeeded",
	}
	for _, trigger := range triggers {
		t.Run(trigger, func(t *testing.T) {
			ev := baseTestEvent(trigger)
			n, err := Build(map[string]event.Event{ev.AppName: ev})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if got := n.Items[0].TargetRevision; got != "v1.0.0" {
				t.Errorf("TargetRevision = %q, want v1.0.0", got)
			}
		})
	}
}

func TestBuild_DeployedDefaultsInitiatedByToAutoSync(t *testing.T) {
	ev := baseTestEvent("on-deployed")
	n, err := Build(map[string]event.Event{ev.AppName: ev})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if n.Items[0].TriggeredBy != "auto-sync" {
		t.Errorf("TriggeredBy = %q, want auto-sync", n.Items[0].TriggeredBy)
	}
}

func TestBuild_DeletedHasNoLink(t *testing.T) {
	ev := baseTestEvent("on-deleted")
	n, err := Build(map[string]event.Event{ev.AppName: ev})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if n.Items[0].Link != "" {
		t.Errorf("Link = %q, want empty for on-deleted", n.Items[0].Link)
	}
}

func TestBuild_SyncFailedTruncatesDetailText(t *testing.T) {
	ev := baseTestEvent("on-sync-failed")
	ev.SyncPhase = "Failed"
	ev.OperationMsg = strings.Repeat("x", 400)

	n, err := Build(map[string]event.Event{ev.AppName: ev})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	item := n.Items[0]
	if len(item.DetailText) != 303 || !strings.HasSuffix(item.DetailText, "...") {
		t.Fatalf("DetailText length = %d, want 303 with ellipsis", len(item.DetailText))
	}
	if len(item.Fields) != 1 || item.Fields[0].Label != "Phase" || item.Fields[0].Value != "Failed" {
		t.Fatalf("Fields = %+v", item.Fields)
	}
}

func TestBuild_HealthDegraded(t *testing.T) {
	ev := baseTestEvent("on-health-degraded")
	n, err := Build(map[string]event.Event{ev.AppName: ev})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	item := n.Items[0]
	if len(item.Fields) != 1 || item.Fields[0].Label != "Health" {
		t.Fatalf("Fields = %+v", item.Fields)
	}
}

func TestBuild_UnknownTriggerSkippedButCounted(t *testing.T) {
	known := baseTestEvent("on-deployed")
	known.AppName = "app-a"
	unknown := baseTestEvent("on-mystery")
	unknown.AppName = "app-b"

	n, err := Build(map[string]event.Event{known.AppName: known, unknown.AppName: unknown})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(n.Items) != 1 {
		t.Fatalf("expected unknown trigger skipped from items, got %d", len(n.Items))
	}
	if !strings.Contains(n.Summary, "mystery") {
		t.Fatalf("expected summary to still count unknown trigger, got %q", n.Summary)
	}
}

func TestBuild_SortedByAppName(t *testing.T) {
	perApp := map[string]event.Event{
		"zebra": {GroupKey: "tatooine", AppName: "zebra", Trigger: "on-deployed"},
		"alpha": {GroupKey: "tatooine", AppName: "alpha", Trigger: "on-deployed"},
		"mid":   {GroupKey: "tatooine", AppName: "mid", Trigger: "on-deployed"},
	}

	for range 5 {
		n, err := Build(perApp)
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if n.Items[0].AppName != "alpha" {
			t.Fatalf("expected first item to be 'alpha', got %q", n.Items[0].AppName)
		}
	}
}

func TestCommitURLAndSHA(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		revision string
		wantURL  string
		wantSHA  string
	}{
		{
			name:     "trims .git and shortens revision",
			repoURL:  "https://github.com/timescale/savannah-tatooine.git",
			revision: "abcdef1234567",
			wantURL:  "https://github.com/timescale/savannah-tatooine/commit/abcdef1234567",
			wantSHA:  "abcdef12",
		},
		{name: "empty repo means no url", repoURL: "", revision: "abcdef1234567", wantURL: "", wantSHA: "abcdef12"},
		{name: "empty revision means no url and no sha", repoURL: "https://example.com/repo.git", revision: "", wantURL: "", wantSHA: "?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := event.Event{RepoURL: tt.repoURL, Revision: tt.revision}
			if got := commitURL(ev); got != tt.wantURL {
				t.Errorf("commitURL() = %q, want %q", got, tt.wantURL)
			}
			if got := commitSHA(ev); got != tt.wantSHA {
				t.Errorf("commitSHA() = %q, want %q", got, tt.wantSHA)
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
