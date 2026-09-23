package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

const defaultBaseURL = "https://slack.com/api"

var (
	_ notification.Backend       = (*Client)(nil)
	_ notification.Updater       = (*Client)(nil)
	_ notification.ThreadReplier = (*Client)(nil)
)

type Client struct {
	httpClient *http.Client
	token      string
	baseURL    string
	maxRetries int
	renderer   Renderer
}

type Option func(*Client)

// WithBaseURL overrides the default https://slack.com/api — needed for
// Slack Enterprise Grid / GovSlack deployments on a different domain.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = baseURL }
}

// WithRenderer overrides the built-in per-trigger rendering (DefaultRenderer)
// — e.g. with a TemplateRenderer, or a fallback wrapper around one.
func WithRenderer(r Renderer) Option {
	return func(c *Client) { c.renderer = r }
}

func NewClient(token string, timeout time.Duration, maxRetries int, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: timeout},
		token:      token,
		baseURL:    defaultBaseURL,
		maxRetries: maxRetries,
		renderer:   DefaultRenderer{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type postMessageRequest struct {
	Channel     string          `json:"channel"`
	ThreadTS    string          `json:"thread_ts,omitempty"`
	Text        string          `json:"text"`
	Attachments json.RawMessage `json:"attachments,omitempty"`
}

type updateMessageRequest struct {
	Channel     string          `json:"channel"`
	TS          string          `json:"ts"`
	Text        string          `json:"text"`
	Attachments json.RawMessage `json:"attachments,omitempty"`
}

type apiResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	TS      string `json:"ts"`
	Channel string `json:"channel"`
}

// refSeparator joins the channel ID Slack resolved at post time with the
// message ts into one opaque ref string. chat.update requires the actual
// channel ID — unlike chat.postMessage, it doesn't accept a channel name —
// so later calls use the ID Slack itself returned rather than trusting
// whatever recipient string was originally configured (which may well be a
// human-friendly name).
const refSeparator = "|"

func encodeRef(channel, ts string) string {
	return channel + refSeparator + ts
}

// decodeRef splits a ref produced by encodeRef back into (channel, ts). A
// ref without the separator predates this format; fall back to recipient
// as the channel, best-effort, rather than failing outright.
func decodeRef(ref, recipient string) (channel, ts string) {
	channel, ts, ok := strings.Cut(ref, refSeparator)
	if !ok {
		return recipient, ref
	}
	return channel, ts
}

// Post, Update, and PostThreadReply implement notification.Backend,
// notification.Updater, and notification.ThreadReplier respectively.

func (c *Client) Post(ctx context.Context, recipient string, n notification.Notification) (string, error) {
	text, attachments, err := c.renderer.Render(n)
	if err != nil {
		return "", fmt.Errorf("render notification: %w", err)
	}
	out, err := c.call(ctx, "chat.postMessage", postMessageRequest{Channel: recipient, Text: text, Attachments: attachments})
	if err != nil {
		return "", err
	}
	return encodeRef(out.Channel, out.TS), nil
}

func (c *Client) Update(ctx context.Context, recipient, ref string, n notification.Notification) error {
	text, attachments, err := c.renderer.Render(n)
	if err != nil {
		return fmt.Errorf("render notification: %w", err)
	}
	channel, ts := decodeRef(ref, recipient)
	_, err = c.call(ctx, "chat.update", updateMessageRequest{Channel: channel, TS: ts, Text: text, Attachments: attachments})
	return err
}

func (c *Client) PostThreadReply(ctx context.Context, recipient, ref, text string) error {
	channel, ts := decodeRef(ref, recipient)
	_, err := c.call(ctx, "chat.postMessage", postMessageRequest{Channel: channel, ThreadTS: ts, Text: text})
	return err
}

func (c *Client) call(ctx context.Context, method string, body any) (apiResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return apiResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff(attempt)):
			case <-ctx.Done():
				return apiResponse{}, ctx.Err()
			}
		}

		out, retryable, err := c.attempt(ctx, method, b)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retryable {
			return apiResponse{}, err
		}
	}

	return apiResponse{}, lastErr
}

func (c *Client) attempt(ctx context.Context, method string, body []byte) (out apiResponse, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return apiResponse{}, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return apiResponse{}, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return apiResponse{}, true, fmt.Errorf("decode response: %w", err)
	}

	if out.OK {
		return out, false, nil
	}

	err = fmt.Errorf("slack api error: %s", out.Error)
	return apiResponse{}, isRetryable(resp.StatusCode, out.Error), err
}

func isRetryable(statusCode int, apiErr string) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500 || apiErr == "ratelimited"
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 200 * time.Millisecond
	if d > 2*time.Second {
		return 2 * time.Second
	}
	return d
}
