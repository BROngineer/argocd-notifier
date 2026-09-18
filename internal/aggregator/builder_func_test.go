package aggregator

import (
	"testing"

	"github.com/BROngineer/argocd-notifier/internal/event"
	"github.com/BROngineer/argocd-notifier/internal/render"
)

func TestMessageBuilderFunc_Build(t *testing.T) {
	var got map[string]event.Event
	fn := MessageBuilderFunc(func(perApp map[string]event.Event) (render.Message, error) {
		got = perApp
		return render.Message{Text: "ok"}, nil
	})

	var builder MessageBuilder = fn
	perApp := map[string]event.Event{"app": baseEvent()}

	msg, err := builder.Build(perApp)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if msg.Text != "ok" {
		t.Fatalf("Text = %q, want ok", msg.Text)
	}
	if len(got) != 1 {
		t.Fatalf("expected underlying func to receive perApp, got %v", got)
	}
}
