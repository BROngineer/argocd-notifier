package notification

import "context"

type Field struct {
	Label string
	Value string
}

// Item is one app's row in an aggregated notification. Fields that are
// structured enough for a backend to render its own way (a commit link, an
// "Open in ArgoCD" button) get dedicated fields instead of living in Fields,
// so no backend-specific markup (e.g. Slack's "<url|text>") leaks into the
// domain model.
type Item struct {
	AppName     string
	Cluster     string
	Trigger     string
	CommitURL   string
	CommitSHA   string
	TriggeredBy string
	Images      []string
	Fields      []Field
	DetailText  string
	Link        string
}

type Notification struct {
	Summary string
	Items   []Item
}

// Backend is the one capability every notification target must have:
// send something new. Backends that can also edit a previously sent
// notification, or reply in a thread under one, implement Updater and/or
// ThreadReplier as well — checked via type assertion, not required here.
type Backend interface {
	Post(ctx context.Context, recipient string, n Notification) (ref string, err error)
}

type Updater interface {
	Update(ctx context.Context, recipient, ref string, n Notification) error
}

type ThreadReplier interface {
	PostThreadReply(ctx context.Context, recipient, ref, text string) error
}

// Notifier is a single-call alternative to Backend+Updater: the backend
// decides for itself whether to post fresh or edit in place, and always
// returns whatever ref now represents the result — which may differ from
// the ref passed in. A backend implementing this is preferred over
// Post/Update when both are present, since Update has no way to report a
// changed ref back to the caller.
type Notifier interface {
	Notify(ctx context.Context, recipient, ref string, n Notification) (newRef string, err error)
}
