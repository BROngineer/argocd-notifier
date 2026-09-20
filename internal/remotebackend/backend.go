package remotebackend

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/internal/notification"
	"github.com/BROngineer/argocd-notifier/internal/registry"
)

var (
	ErrBackendNotRegistered    = errors.New("BackendNotRegistered")
	ErrThreadReplyNotSupported = errors.New("ThreadReplyNotSupported")
)

// Backend adapts one named entry in the registry to notification.Notifier
// and notification.ThreadReplier. It never caches the registry lookup: a
// backend's baseURL can change on re-registration, and supportsThreadReply
// is a live capability, not a fixed one, so every call re-reads current
// state instead of trusting whatever was true when this adapter was built.
type Backend struct {
	name string
	reg  *registry.Registry
	http *http.Client
}

func New(name string, reg *registry.Registry, httpClient *http.Client) *Backend {
	return &Backend{name: name, reg: reg, http: httpClient}
}

var (
	_ notification.Notifier      = (*Backend)(nil)
	_ notification.ThreadReplier = (*Backend)(nil)
)

func (b *Backend) Notify(ctx context.Context, recipient, ref string, n notification.Notification) (string, error) {
	be, ok := b.reg.Lookup(b.name)
	if !ok {
		return "", ErrBackendNotRegistered
	}

	client, err := backendapi.NewClientWithResponses(be.BaseURL, backendapi.WithHTTPClient(b.http))
	if err != nil {
		return "", fmt.Errorf("build client for backend %q: %w", b.name, err)
	}

	resp, err := client.NotifyWithResponse(ctx, backendapi.NotifyRequest{
		Recipient:    recipient,
		Ref:          ref,
		Notification: toWireNotification(n),
	})
	if err != nil {
		return "", fmt.Errorf("notify %q: %w", b.name, err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return "", fmt.Errorf("notify %q: unexpected status %d: %s", b.name, resp.StatusCode(), resp.Body)
	}

	return resp.JSON200.Ref, nil
}

func (b *Backend) PostThreadReply(ctx context.Context, recipient, ref, text string) error {
	be, ok := b.reg.Lookup(b.name)
	if !ok {
		return ErrBackendNotRegistered
	}
	if !be.SupportsThreadReply {
		return ErrThreadReplyNotSupported
	}

	client, err := backendapi.NewClientWithResponses(be.BaseURL, backendapi.WithHTTPClient(b.http))
	if err != nil {
		return fmt.Errorf("build client for backend %q: %w", b.name, err)
	}

	resp, err := client.ThreadReplyWithResponse(ctx, backendapi.ThreadReplyRequest{
		Recipient: recipient,
		Ref:       ref,
		Text:      text,
	})
	if err != nil {
		return fmt.Errorf("thread reply %q: %w", b.name, err)
	}
	if resp.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("thread reply %q: unexpected status %d: %s", b.name, resp.StatusCode(), resp.Body)
	}

	return nil
}

func toWireNotification(n notification.Notification) backendapi.Notification {
	items := make([]backendapi.Item, len(n.Items))
	for i, item := range n.Items {
		items[i] = toWireItem(item)
	}
	return backendapi.Notification{Summary: n.Summary, Items: items}
}

func toWireItem(item notification.Item) backendapi.Item {
	wire := backendapi.Item{
		AppName: item.AppName,
		Cluster: item.Cluster,
		Trigger: item.Trigger,
	}
	if item.CommitSHA != "" {
		wire.CommitSHA = new(item.CommitSHA)
	}
	if item.CommitURL != "" {
		wire.CommitURL = new(item.CommitURL)
	}
	if item.DetailText != "" {
		wire.DetailText = new(item.DetailText)
	}
	if item.Link != "" {
		wire.Link = new(item.Link)
	}
	if item.TriggeredBy != "" {
		wire.TriggeredBy = new(item.TriggeredBy)
	}
	if len(item.Images) > 0 {
		wire.Images = new(item.Images)
	}
	if len(item.Fields) > 0 {
		fields := make([]backendapi.Field, len(item.Fields))
		for i, f := range item.Fields {
			fields[i] = backendapi.Field{Label: f.Label, Value: f.Value}
		}
		wire.Fields = new(fields)
	}
	return wire
}
