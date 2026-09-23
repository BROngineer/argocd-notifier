package slack

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeTemplate(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

func TestTemplateWatcher_RenderBeforeAnyLoad_UsesDefaultRenderer(t *testing.T) {
	w := NewTemplateWatcher(filepath.Join(t.TempDir(), "missing.tmpl"), false, testLogger())

	item := notification.Item{AppName: "app-a", Trigger: "on-deployed"}
	text, attachments, err := w.Render(notification.Notification{Summary: "s", Items: []notification.Item{item}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	wantText, wantAttachments, _ := DefaultRenderer{}.Render(notification.Notification{Summary: "s", Items: []notification.Item{item}})
	if text != wantText || string(attachments) != string(wantAttachments) {
		t.Fatalf("Render() = (%q, %s), want DefaultRenderer's output (%q, %s)", text, attachments, wantText, wantAttachments)
	}
}

func TestTemplateWatcher_ReloadLoadsValidTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.tmpl")
	writeTemplate(t, path, simpleTemplate)

	w := NewTemplateWatcher(path, false, testLogger())
	w.Reload()

	text, attachments, err := w.Render(notification.Notification{Summary: "hi", Items: []notification.Item{{AppName: "app-a"}}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if text != "hi" || !strings.Contains(string(attachments), "app-a") {
		t.Fatalf("Render() = (%q, %s), want the custom template's output", text, attachments)
	}
}

func TestTemplateWatcher_ReloadWithBadTemplate_KeepsPrevious(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.tmpl")
	writeTemplate(t, path, simpleTemplate)

	w := NewTemplateWatcher(path, false, testLogger())
	w.Reload()

	// Overwrite with something that fails to parse, but keep the same
	// mtime as a broken write might (e.g. a fast editor save) — Reload
	// must still detect and reject it, not skip it as "unchanged".
	writeTemplate(t, path, `{{define "text"}}{{.Oops`)
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	w.Reload()

	text, _, err := w.Render(notification.Notification{Summary: "hi"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if text != "hi" {
		t.Fatalf("text = %q, want the previous good template's output (%q)", text, "hi")
	}
}

func TestTemplateWatcher_ReloadSkipsUnchangedMtime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.tmpl")
	writeTemplate(t, path, simpleTemplate)

	w := NewTemplateWatcher(path, false, testLogger())
	w.Reload()
	loaded := w.current.Load()

	// Same mtime as before, even though content is now broken — Reload
	// should skip re-parsing entirely, so the good renderer stays in place.
	same := w.modTime
	writeTemplate(t, path, `{{define "text"}}{{.Oops`)
	if err := os.Chtimes(path, same, same); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	w.Reload()

	if w.current.Load() != loaded {
		t.Fatal("Reload() replaced the renderer despite an unchanged mtime")
	}
}

func TestTemplateWatcher_Render_ExecutionError_FallbackDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.tmpl")
	writeTemplate(t, path, `{{define "text"}}{{.NoSuchField}}{{end}}{{define "attachments"}}[]{{end}}`)

	w := NewTemplateWatcher(path, false, testLogger())
	w.Reload()

	if _, _, err := w.Render(notification.Notification{Summary: "s"}); err == nil {
		t.Fatal("expected error to propagate when fallbackToDefault=false")
	}
}

func TestTemplateWatcher_Render_ExecutionError_FallbackEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.tmpl")
	writeTemplate(t, path, `{{define "text"}}{{.NoSuchField}}{{end}}{{define "attachments"}}[]{{end}}`)

	w := NewTemplateWatcher(path, true, testLogger())
	w.Reload()

	item := notification.Item{AppName: "app-a", Trigger: "on-deployed"}
	text, attachments, err := w.Render(notification.Notification{Summary: "s", Items: []notification.Item{item}})
	if err != nil {
		t.Fatalf("Render() error = %v, want fallback to DefaultRenderer instead of an error", err)
	}
	wantText, wantAttachments, _ := DefaultRenderer{}.Render(notification.Notification{Summary: "s", Items: []notification.Item{item}})
	if text != wantText || string(attachments) != string(wantAttachments) {
		t.Fatalf("Render() = (%q, %s), want DefaultRenderer's output", text, attachments)
	}
}

func TestTemplateWatcher_Run_ReloadsOnTick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.tmpl")
	writeTemplate(t, path, `{{define "text"}}v1{{end}}{{define "attachments"}}[]{{end}}`)

	w := NewTemplateWatcher(path, false, testLogger())
	ctx := t.Context()
	go w.Run(ctx, 10*time.Millisecond)

	waitFor(t, time.Second, func() bool {
		text, _, err := w.Render(notification.Notification{})
		return err == nil && text == "v1"
	})

	future := time.Now().Add(time.Second)
	writeTemplate(t, path, `{{define "text"}}v2{{end}}{{define "attachments"}}[]{{end}}`)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	waitFor(t, time.Second, func() bool {
		text, _, err := w.Render(notification.Notification{})
		return err == nil && text == "v2"
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
