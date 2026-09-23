package slackbackend

import (
	"reflect"
	"testing"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/internal/notification"
)

func TestFromWireItem_OmitsUnsetOptionalFields(t *testing.T) {
	got := fromWireItem(backendapi.Item{AppName: "a", Cluster: "c", Trigger: "on-deployed"})
	want := notification.Item{AppName: "a", Cluster: "c", Trigger: "on-deployed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fromWireItem() = %+v, want %+v", got, want)
	}
}

func TestFromWireItem_SetsOptionalFields(t *testing.T) {
	wire := backendapi.Item{
		AppName: "a", Cluster: "c", Trigger: "on-deployed",
		CommitSHA: new("sha1"), CommitURL: new("https://x"), TargetRevision: new("v0.21.0"), DetailText: new("detail"), Link: new("https://y"),
		TriggeredBy: new("someone"), Images: new([]string{"img1"}), Fields: new([]backendapi.Field{{Label: "l", Value: "v"}}),
	}
	got := fromWireItem(wire)
	want := notification.Item{
		AppName: "a", Cluster: "c", Trigger: "on-deployed",
		CommitSHA: "sha1", CommitURL: "https://x", TargetRevision: "v0.21.0", DetailText: "detail", Link: "https://y",
		TriggeredBy: "someone", Images: []string{"img1"}, Fields: []notification.Field{{Label: "l", Value: "v"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fromWireItem() = %+v, want %+v", got, want)
	}
}

func TestFromWireNotification(t *testing.T) {
	wire := backendapi.Notification{
		Summary: "s",
		Items:   []backendapi.Item{{AppName: "a", Cluster: "c", Trigger: "on-deployed"}},
	}
	got := fromWireNotification(wire)
	want := notification.Notification{
		Summary: "s",
		Items:   []notification.Item{{AppName: "a", Cluster: "c", Trigger: "on-deployed"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fromWireNotification() = %+v, want %+v", got, want)
	}
}
