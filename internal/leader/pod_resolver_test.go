package leader

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func fakePod(name, ip string) *corev1.Pod {
	return &corev1.Pod{
		Name: name, Namespace: "default",
		Status: corev1.PodStatus{PodIP: ip},
	}
}

func TestPodAddressResolver_Resolve(t *testing.T) {
	client := fake.NewSimpleClientset(fakePod("pod-a", "10.244.0.1"))
	r := NewPodAddressResolver(client, "default")

	addr, err := r.Resolve(context.Background(), "pod-a")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if addr != "10.244.0.1" {
		t.Fatalf("Resolve() = %q, want 10.244.0.1", addr)
	}
}

func TestPodAddressResolver_UnknownPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := NewPodAddressResolver(client, "default")

	if _, err := r.Resolve(context.Background(), "pod-a"); err == nil {
		t.Fatal("Resolve() error = nil, want error for unknown pod")
	}
}

func TestPodAddressResolver_NoIPYet(t *testing.T) {
	client := fake.NewSimpleClientset(fakePod("pod-a", ""))
	r := NewPodAddressResolver(client, "default")

	if _, err := r.Resolve(context.Background(), "pod-a"); err == nil {
		t.Fatal("Resolve() error = nil, want error for a pod with no IP yet")
	}
}

func TestPodAddressResolver_CachesUntilIdentityChanges(t *testing.T) {
	client := fake.NewSimpleClientset(fakePod("pod-a", "10.244.0.1"), fakePod("pod-b", "10.244.0.2"))
	r := NewPodAddressResolver(client, "default")

	if _, err := r.Resolve(context.Background(), "pod-a"); err != nil {
		t.Fatalf("Resolve(pod-a) #1 error = %v", err)
	}
	if _, err := r.Resolve(context.Background(), "pod-a"); err != nil {
		t.Fatalf("Resolve(pod-a) #2 error = %v", err)
	}

	getActions := 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "get" {
			getActions++
		}
	}
	if getActions != 1 {
		t.Fatalf("get API calls = %d, want 1 (second Resolve for the same identity should be cached)", getActions)
	}

	addr, err := r.Resolve(context.Background(), "pod-b")
	if err != nil {
		t.Fatalf("Resolve(pod-b) error = %v", err)
	}
	if addr != "10.244.0.2" {
		t.Fatalf("Resolve(pod-b) = %q, want 10.244.0.2", addr)
	}

	getActions = 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "get" {
			getActions++
		}
	}
	if getActions != 2 {
		t.Fatalf("get API calls after identity change = %d, want 2", getActions)
	}
}
