package leaderproxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Handler routes a request to a local handler when this instance is the
// leader, or reverse-proxies it to whichever instance currently is when
// it isn't — so every replica can be a normal, always-Ready Service
// endpoint while still guaranteeing exactly one instance ever actually
// processes a request (the in-memory aggregation/registry state only
// exists, and only matters, on the leader).
type Handler struct {
	isLeader   func() bool
	leaderAddr func() (string, bool)
	local      http.Handler
	proxy      *httputil.ReverseProxy
}

// New builds a Handler. leaderAddr returns the current leader's base URL
// (e.g. "http://pod-a.argocd-notifier-headless.argocd.svc.cluster.local:8080")
// and false if no leader is currently known. timeout bounds how long a
// proxied request waits for the leader's response headers.
func New(isLeader func() bool, leaderAddr func() (string, bool), local http.Handler, timeout time.Duration) *Handler {
	h := &Handler{isLeader: isLeader, leaderAddr: leaderAddr, local: local}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			addr, _ := leaderAddr() // ServeHTTP already checked ok before ever calling the proxy
			u, err := url.Parse(addr)
			if err != nil {
				return // Rewrite has no error path; a bad address surfaces as a downstream dial failure
			}
			pr.SetURL(u)
		},
		Transport: &http.Transport{ResponseHeaderTimeout: timeout},
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.isLeader() {
		h.local.ServeHTTP(w, r)
		return
	}
	if _, ok := h.leaderAddr(); !ok {
		http.Error(w, "no leader currently known", http.StatusServiceUnavailable)
		return
	}
	h.proxy.ServeHTTP(w, r)
}
