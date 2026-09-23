package notification

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

var ErrNoEvents = errors.New("NoEvents")

type itemBuilder func(ev event.Event) Item

var triggerBuilders = map[string]itemBuilder{
	"on-created":             buildCreatedItem,
	"on-deleted":             buildDeletedItem,
	"on-deployed":            buildDeployedItem,
	"on-health-degraded":     buildHealthDegradedItem,
	"on-sync-failed":         buildSyncFailedItem,
	"on-sync-running":        buildSyncRunningItem,
	"on-sync-status-unknown": buildSyncStatusUnknownItem,
	"on-sync-succeeded":      buildSyncSucceededItem,
}

func Build(perApp map[string]event.Event) (Notification, error) {
	if len(perApp) == 0 {
		return Notification{}, ErrNoEvents
	}

	names := make([]string, 0, len(perApp))
	counts := make(map[string]int, len(perApp))
	var groupKey string
	for name, ev := range perApp {
		names = append(names, name)
		counts[ev.Trigger]++
		groupKey = ev.GroupKey
	}
	sort.Strings(names)

	items := make([]Item, 0, len(names))
	for _, name := range names {
		build, ok := triggerBuilders[perApp[name].Trigger]
		if !ok {
			continue
		}
		items = append(items, build(perApp[name]))
	}

	return Notification{
		Summary: summary(groupKey, counts),
		Items:   items,
	}, nil
}

func summary(groupKey string, counts map[string]int) string {
	triggers := make([]string, 0, len(counts))
	for t := range counts {
		triggers = append(triggers, t)
	}
	sort.Strings(triggers)

	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		parts = append(parts, fmt.Sprintf("%d %s", counts[t], strings.TrimPrefix(t, "on-")))
	}

	return fmt.Sprintf("%s — %s", groupKey, strings.Join(parts, ", "))
}

func buildCreatedItem(ev event.Event) Item {
	return Item{AppName: ev.AppName, Cluster: cluster(ev), Trigger: ev.Trigger, TargetRevision: ev.TargetRevision, Link: argoCDLink(ev)}
}

// buildDeletedItem leaves Link empty — the Application no longer exists to open.
func buildDeletedItem(ev event.Event) Item {
	return Item{AppName: ev.AppName, Cluster: cluster(ev), Trigger: ev.Trigger, TargetRevision: ev.TargetRevision}
}

func buildDeployedItem(ev event.Event) Item {
	return Item{
		AppName:        ev.AppName,
		Cluster:        cluster(ev),
		Trigger:        ev.Trigger,
		CommitURL:      commitURL(ev),
		CommitSHA:      commitSHA(ev),
		TargetRevision: ev.TargetRevision,
		TriggeredBy:    orDefault(ev.InitiatedBy, "auto-sync"),
		Images:         ev.Images,
		Link:           argoCDLink(ev),
	}
}

func buildSyncFailedItem(ev event.Event) Item {
	return Item{
		AppName:        ev.AppName,
		Cluster:        cluster(ev),
		Trigger:        ev.Trigger,
		TargetRevision: ev.TargetRevision,
		Fields:         []Field{{Label: "Phase", Value: ev.SyncPhase}},
		DetailText:     truncate(ev.OperationMsg, 300),
		Link:           argoCDLink(ev),
	}
}

func buildHealthDegradedItem(ev event.Event) Item {
	return Item{
		AppName:        ev.AppName,
		Cluster:        cluster(ev),
		Trigger:        ev.Trigger,
		TargetRevision: ev.TargetRevision,
		Fields:         []Field{{Label: "Health", Value: ":large_yellow_circle: Degraded"}},
		Link:           argoCDLink(ev),
	}
}

func buildSyncRunningItem(ev event.Event) Item {
	return Item{
		AppName:        ev.AppName,
		Cluster:        cluster(ev),
		Trigger:        ev.Trigger,
		CommitURL:      commitURL(ev),
		CommitSHA:      commitSHA(ev),
		TargetRevision: ev.TargetRevision,
		TriggeredBy:    orDefault(ev.InitiatedBy, "auto-sync"),
		Link:           argoCDLink(ev),
	}
}

func buildSyncStatusUnknownItem(ev event.Event) Item {
	return Item{
		AppName:        ev.AppName,
		Cluster:        cluster(ev),
		Trigger:        ev.Trigger,
		TargetRevision: ev.TargetRevision,
		Fields:         []Field{{Label: "Sync Status", Value: orDefault(ev.SyncStatus, "Unknown")}},
		Link:           argoCDLink(ev),
	}
}

func buildSyncSucceededItem(ev event.Event) Item {
	return Item{
		AppName:        ev.AppName,
		Cluster:        cluster(ev),
		Trigger:        ev.Trigger,
		CommitURL:      commitURL(ev),
		CommitSHA:      commitSHA(ev),
		TargetRevision: ev.TargetRevision,
		Link:           argoCDLink(ev),
	}
}

func cluster(ev event.Event) string {
	return orDefault(ev.Target, "?")
}

func argoCDLink(ev event.Event) string {
	return fmt.Sprintf("%s/applications/%s", strings.TrimRight(ev.ArgoCDURL, "/"), ev.AppName)
}

// commitURL is "" when there's nothing to link to — the caller decides
// whether to render a hyperlink or plain text for commitSHA in that case.
func commitURL(ev event.Event) string {
	if ev.RepoURL == "" || ev.Revision == "" {
		return ""
	}
	return strings.TrimSuffix(ev.RepoURL, ".git") + "/commit/" + ev.Revision
}

func commitSHA(ev event.Event) string {
	if ev.Revision == "" {
		return "?"
	}
	if len(ev.Revision) > 8 {
		return ev.Revision[:8]
	}
	return ev.Revision
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
