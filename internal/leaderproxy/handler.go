package leaderproxy

import (
	"log/slog"
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
	logger     *slog.Logger
	proxy      *httputil.ReverseProxy
}

// New builds a Handler. leaderAddr returns the current leader's base URL
// (e.g. "http://10.244.0.12:8080") and false if no leader is currently
// known. timeout bounds how long a proxied request waits for the leader's
// response headers.
func New(isLeader func() bool, leaderAddr func() (string, bool), local http.Handler, timeout time.Duration, logger *slog.Logger) *Handler {
	h := &Handler{isLeader: isLeader, leaderAddr: leaderAddr, local: local, logger: logger}
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
		// Without this, a dial/proxy failure logs through the stdlib log
		// package's default logger — plain text, inconsistent with every
		// other log line this process emits via slog.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.isLeader() {
		h.local.ServeHTTP(w, r)
		return
	}
	addr, ok := h.leaderAddr()
	if !ok {
		h.logger.Warn("no leader currently known, dropping request", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "no leader currently known", http.StatusServiceUnavailable)
		return
	}
	// Debug, not Info: this fires on every request from every non-leader
	// replica, so it stays quiet by default — failures above/below this
	// (unknown leader, dial/proxy errors) are the parts worth seeing.
	h.logger.Debug("forwarding to leader", "method", r.Method, "path", r.URL.Path, "leaderAddr", addr)
	h.proxy.ServeHTTP(w, r)
}
