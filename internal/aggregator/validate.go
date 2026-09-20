package aggregator

import "github.com/BROngineer/argocd-notifier/internal/notification"

// ValidateStaticBackend checks whether the compiled-in backend selected via
// BACKEND=<name> supports what duplicateAction requires — called once at
// startup so a misconfigured deployment fails fast, before wiring the rest
// of the process, rather than silently dropping thread-replies later.
//
// Only the compiled-in backend can be checked this way: a backend that
// only exists via runtime registration isn't known yet at startup, so this
// guarantee doesn't (and can't) extend to the dynamic routing path —
// remotebackend.Backend.PostThreadReply checks that per-call instead.
func ValidateStaticBackend(duplicateAction DuplicateAction, backend any) error {
	if duplicateAction != DuplicateActionThread {
		return nil
	}
	if _, ok := backend.(notification.ThreadReplier); !ok {
		return ErrBackendDoesNotSupportThreadReply
	}
	return nil
}
