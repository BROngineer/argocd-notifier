package leader

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodAddressResolver resolves a leader's identity (its pod name) to that
// pod's current IP via the Kubernetes API — used to address a request at
// the leader when DNS-based resolution isn't available (a Deployment can't
// give each replica a unique, stable hostname the way a StatefulSet can,
// so per-pod DNS records aren't an option here).
//
// The namespace is the pods' own namespace, which is not necessarily the
// same as the Lease's namespace (leader.Config.Namespace) — those are
// independent by design.
type PodAddressResolver struct {
	client    kubernetes.Interface
	namespace string

	mu       sync.Mutex
	identity string
	addr     string
}

func NewPodAddressResolver(client kubernetes.Interface, namespace string) *PodAddressResolver {
	return &PodAddressResolver{client: client, namespace: namespace}
}

// Resolve returns the pod's current IP, caching it until identity changes
// — avoids one Kubernetes API call per forwarded request; a leadership
// change (rare) is what invalidates the cache, not each request.
func (r *PodAddressResolver) Resolve(ctx context.Context, identity string) (string, error) {
	r.mu.Lock()
	if identity == r.identity && r.addr != "" {
		addr := r.addr
		r.mu.Unlock()
		return addr, nil
	}
	r.mu.Unlock()

	pod, err := r.client.CoreV1().Pods(r.namespace).Get(ctx, identity, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod %q: %w", identity, err)
	}
	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("pod %q has no IP yet", identity)
	}

	r.mu.Lock()
	r.identity = identity
	r.addr = pod.Status.PodIP
	r.mu.Unlock()

	return pod.Status.PodIP, nil
}
