package backendapi

import (
	"encoding/json"
	"testing"
)

func TestNotifyRequest_RoundTrip(t *testing.T) {
	req := NotifyRequest{
		Recipient: "chan1",
		Ref:       "ts-123",
		Notification: Notification{
			Summary: "camel — 1 deployed",
			Items: []Item{
				{AppName: "camel-dev", Cluster: "dev-eu-central-1", Trigger: "on-deployed", Link: new("https://argocd.example.com/applications/camel-dev")},
			},
		},
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got NotifyRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Recipient != req.Recipient || got.Ref != req.Ref || len(got.Notification.Items) != 1 {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, req)
	}
	if got.Notification.Items[0].AppName != "camel-dev" {
		t.Fatalf("item round-trip mismatch: got %+v", got.Notification.Items[0])
	}
}
