package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRegisterBackendRequest_RoundTrip(t *testing.T) {
	req := RegisterBackendRequest{
		Name:                "slack",
		BaseURL:             "http://argocd-notifier-slack.default.svc.cluster.local:8080",
		SupportsThreadReply: true,
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got RegisterBackendRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got != req {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, req)
	}
}

func TestEvent_RoundTrip_RequiredOnly(t *testing.T) {
	ev := Event{
		GroupKey:  "tatooine",
		AppName:   "tatooine-dev",
		Trigger:   "on-deployed",
		Revision:  "rev-1",
		Recipient: "chan1",
		Backend:   "slack",
	}

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got Event
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, ev) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, ev)
	}
}

func TestEvent_RoundTrip_WithOptionalFields(t *testing.T) {
	ev := Event{
		GroupKey:         "tatooine",
		AppName:          "tatooine-dev",
		Trigger:          "on-health-degraded",
		Revision:         "rev-1",
		Recipient:        "chan1",
		Backend:          "slack",
		Labels:           new(map[string]string{"application/name": "tatooine"}),
		Target:           new("dev-eu-central-1"),
		HealthStatus:     new("Degraded"),
		SyncPhase:        new("Succeeded"),
		SyncStatus:       new("Synced"),
		OperationMessage: new("sync failed"),
		RepoURL:          new("https://example.com/repo.git"),
		Images:           new([]string{"example.com/app:v1"}),
		InitiatedBy:      new("someone"),
		ArgocdUrl:        new("https://argocd.example.com"),
	}

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got Event
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, ev) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, ev)
	}
}
