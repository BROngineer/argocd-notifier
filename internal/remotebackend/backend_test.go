package remotebackend

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/internal/notification"
	"github.com/BROngineer/argocd-notifier/internal/registry"
)

type fakeServer struct {
	*httptest.Server
	notifyCalls       atomic.Int32
	threadReplyCalls  atomic.Int32
	lastNotify        backendapi.NotifyRequest
	lastThreadReply   backendapi.ThreadReplyRequest
	notifyStatus      int
	notifyRef         string
	threadReplyStatus int
}

func newFakeServer() *fakeServer {
	fs := &fakeServer{notifyStatus: http.StatusOK, notifyRef: "ref-1", threadReplyStatus: http.StatusNoContent}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /notify", func(w http.ResponseWriter, r *http.Request) {
		fs.notifyCalls.Add(1)
		_ = json.NewDecoder(r.Body).Decode(&fs.lastNotify)
		if fs.notifyStatus != http.StatusOK {
			w.WriteHeader(fs.notifyStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(backendapi.NotifyResult{Ref: fs.notifyRef})
	})
	mux.HandleFunc("POST /thread-reply", func(w http.ResponseWriter, r *http.Request) {
		fs.threadReplyCalls.Add(1)
		_ = json.NewDecoder(r.Body).Decode(&fs.lastThreadReply)
		w.WriteHeader(fs.threadReplyStatus)
	})
	fs.Server = httptest.NewServer(mux)
	return fs
}

func TestBackend_Notify_Post(t *testing.T) {
	fs := newFakeServer()
	defer fs.Close()
	fs.notifyRef = "msg-1"

	reg := registry.NewRegistry(time.Minute)
	if _, err := reg.Register("slack", fs.URL, false); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	b := New("slack", reg, http.DefaultClient)
	n := notification.Notification{Summary: "s", Items: []notification.Item{{AppName: "a", Cluster: "c", Trigger: "on-deployed"}}}

	ref, err := b.Notify(t.Context(), "chan1", "", n)
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if ref != "msg-1" {
		t.Fatalf("ref = %q, want msg-1", ref)
	}
	if fs.lastNotify.Recipient != "chan1" || fs.lastNotify.Ref != "" {
		t.Fatalf("lastNotify = %+v, want recipient=chan1 ref=\"\"", fs.lastNotify)
	}
}

func TestBackend_Notify_ReturnsNewRefOnEdit(t *testing.T) {
	fs := newFakeServer()
	defer fs.Close()
	fs.notifyRef = "msg-2"

	reg := registry.NewRegistry(time.Minute)
	_, _ = reg.Register("slack", fs.URL, false)
	b := New("slack", reg, http.DefaultClient)

	ref, err := b.Notify(t.Context(), "chan1", "msg-1", notification.Notification{})
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if ref != "msg-2" {
		t.Fatalf("ref = %q, want msg-2 (backend may return a different ref than it was given)", ref)
	}
	if fs.lastNotify.Ref != "msg-1" {
		t.Fatalf("sent ref = %q, want msg-1", fs.lastNotify.Ref)
	}
}

func TestBackend_Notify_UnexpectedStatus(t *testing.T) {
	fs := newFakeServer()
	defer fs.Close()
	fs.notifyStatus = http.StatusInternalServerError

	reg := registry.NewRegistry(time.Minute)
	_, _ = reg.Register("slack", fs.URL, false)
	b := New("slack", reg, http.DefaultClient)

	if _, err := b.Notify(t.Context(), "chan1", "", notification.Notification{}); err == nil {
		t.Fatal("Notify() error = nil, want error for 500 response")
	}
}

func TestBackend_Notify_UnregisteredName(t *testing.T) {
	reg := registry.NewRegistry(time.Minute)
	b := New("slack", reg, http.DefaultClient)

	_, err := b.Notify(t.Context(), "chan1", "", notification.Notification{})
	if !errors.Is(err, ErrBackendNotRegistered) {
		t.Fatalf("Notify() error = %v, want ErrBackendNotRegistered", err)
	}
}

func TestBackend_PostThreadReply_Supported(t *testing.T) {
	fs := newFakeServer()
	defer fs.Close()

	reg := registry.NewRegistry(time.Minute)
	_, _ = reg.Register("slack", fs.URL, true)
	b := New("slack", reg, http.DefaultClient)

	if err := b.PostThreadReply(t.Context(), "chan1", "msg-1", "again"); err != nil {
		t.Fatalf("PostThreadReply() error = %v", err)
	}
	if fs.threadReplyCalls.Load() != 1 {
		t.Fatalf("threadReplyCalls = %d, want 1", fs.threadReplyCalls.Load())
	}
	if fs.lastThreadReply.Ref != "msg-1" || fs.lastThreadReply.Text != "again" {
		t.Fatalf("lastThreadReply = %+v, want ref=msg-1 text=again", fs.lastThreadReply)
	}
}

func TestBackend_PostThreadReply_NotSupported_NoRequestSent(t *testing.T) {
	fs := newFakeServer()
	defer fs.Close()

	reg := registry.NewRegistry(time.Minute)
	_, _ = reg.Register("slack", fs.URL, false)
	b := New("slack", reg, http.DefaultClient)

	err := b.PostThreadReply(t.Context(), "chan1", "msg-1", "again")
	if !errors.Is(err, ErrThreadReplyNotSupported) {
		t.Fatalf("PostThreadReply() error = %v, want ErrThreadReplyNotSupported", err)
	}
	if fs.threadReplyCalls.Load() != 0 {
		t.Fatalf("threadReplyCalls = %d, want 0 (must not call an unsupported endpoint)", fs.threadReplyCalls.Load())
	}
}

func TestBackend_PostThreadReply_CapabilityChangesAcrossReregistration(t *testing.T) {
	fs := newFakeServer()
	defer fs.Close()

	reg := registry.NewRegistry(time.Minute)
	_, _ = reg.Register("slack", fs.URL, false)
	b := New("slack", reg, http.DefaultClient)

	if err := b.PostThreadReply(t.Context(), "chan1", "msg-1", "x"); !errors.Is(err, ErrThreadReplyNotSupported) {
		t.Fatalf("first PostThreadReply() error = %v, want ErrThreadReplyNotSupported", err)
	}

	_, _ = reg.Register("slack", fs.URL, true)

	if err := b.PostThreadReply(t.Context(), "chan1", "msg-1", "x"); err != nil {
		t.Fatalf("second PostThreadReply() error = %v, want nil after capability turned on", err)
	}
}

func TestBackend_Notify_BaseURLRotationOnReregister(t *testing.T) {
	fsA := newFakeServer()
	defer fsA.Close()
	fsA.notifyRef = "from-a"
	fsB := newFakeServer()
	defer fsB.Close()
	fsB.notifyRef = "from-b"

	reg := registry.NewRegistry(time.Minute)
	_, _ = reg.Register("slack", fsA.URL, false)
	b := New("slack", reg, http.DefaultClient)

	ref, err := b.Notify(t.Context(), "chan1", "", notification.Notification{})
	if err != nil || ref != "from-a" {
		t.Fatalf("Notify() = (%q, %v), want (from-a, nil)", ref, err)
	}

	_, _ = reg.Register("slack", fsB.URL, false)

	ref, err = b.Notify(t.Context(), "chan1", "", notification.Notification{})
	if err != nil || ref != "from-b" {
		t.Fatalf("Notify() after re-register = (%q, %v), want (from-b, nil)", ref, err)
	}
}

func TestToWireItem_OmitsUnsetOptionalFields(t *testing.T) {
	wire := toWireItem(notification.Item{AppName: "a", Cluster: "c", Trigger: "on-deployed"})
	if wire.CommitSHA != nil || wire.CommitURL != nil || wire.DetailText != nil || wire.Link != nil || wire.TriggeredBy != nil || wire.Images != nil || wire.Fields != nil {
		t.Fatalf("wire = %+v, want all optional fields nil", wire)
	}
}

func TestToWireItem_SetsOptionalFields(t *testing.T) {
	item := notification.Item{
		AppName: "a", Cluster: "c", Trigger: "on-deployed",
		CommitSHA: "sha1", CommitURL: "https://x", DetailText: "detail", Link: "https://y",
		TriggeredBy: "someone", Images: []string{"img1"}, Fields: []notification.Field{{Label: "l", Value: "v"}},
	}
	wire := toWireItem(item)
	if wire.CommitSHA == nil || *wire.CommitSHA != "sha1" {
		t.Fatalf("CommitSHA = %v, want sha1", wire.CommitSHA)
	}
	if wire.Images == nil || (*wire.Images)[0] != "img1" {
		t.Fatalf("Images = %v, want [img1]", wire.Images)
	}
	if wire.Fields == nil || (*wire.Fields)[0].Label != "l" {
		t.Fatalf("Fields = %v, want [{l v}]", wire.Fields)
	}
}

func TestResolver_Resolve_Registered(t *testing.T) {
	fs := newFakeServer()
	defer fs.Close()

	reg := registry.NewRegistry(time.Minute)
	_, _ = reg.Register("slack", fs.URL, false)
	resolver := NewResolver(reg, http.DefaultClient)

	got, ok := resolver.Resolve("slack")
	if !ok {
		t.Fatal("Resolve() ok = false, want true")
	}
	if _, ok := got.(*Backend); !ok {
		t.Fatalf("Resolve() = %T, want *Backend", got)
	}
}

func TestResolver_Resolve_Unregistered(t *testing.T) {
	reg := registry.NewRegistry(time.Minute)
	resolver := NewResolver(reg, http.DefaultClient)

	if _, ok := resolver.Resolve("slack"); ok {
		t.Fatal("Resolve() ok = true, want false for unregistered name")
	}
}
