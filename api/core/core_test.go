package core

import (
	"encoding/json"
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
