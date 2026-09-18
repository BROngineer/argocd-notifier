package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/render"
)

const defaultBaseURL = "https://slack.com/api"

type Client struct {
	httpClient *http.Client
	token      string
	baseURL    string
	maxRetries int
}

type Option func(*Client)

// WithBaseURL overrides the default https://slack.com/api — needed for
// Slack Enterprise Grid / GovSlack deployments on a different domain.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = baseURL }
}

func NewClient(token string, timeout time.Duration, maxRetries int, opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: timeout},
		token:      token,
		baseURL:    defaultBaseURL,
		maxRetries: maxRetries,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type postMessageRequest struct {
	Channel     string              `json:"channel"`
	ThreadTS    string              `json:"thread_ts,omitempty"`
	Text        string              `json:"text"`
	Attachments []render.Attachment `json:"attachments,omitempty"`
}

type updateMessageRequest struct {
	Channel     string              `json:"channel"`
	TS          string              `json:"ts"`
	Text        string              `json:"text"`
	Attachments []render.Attachment `json:"attachments,omitempty"`
}

type apiResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	TS    string `json:"ts"`
}

func (c *Client) Post(ctx context.Context, channel string, msg render.Message) (string, error) {
	return c.call(ctx, "chat.postMessage", postMessageRequest{Channel: channel, Text: msg.Text, Attachments: msg.Attachments})
}

func (c *Client) Update(ctx context.Context, channel, ts string, msg render.Message) error {
	_, err := c.call(ctx, "chat.update", updateMessageRequest{Channel: channel, TS: ts, Text: msg.Text, Attachments: msg.Attachments})
	return err
}

func (c *Client) PostThreadReply(ctx context.Context, channel, threadTS, text string) error {
	_, err := c.call(ctx, "chat.postMessage", postMessageRequest{Channel: channel, ThreadTS: threadTS, Text: text})
	return err
}

func (c *Client) call(ctx context.Context, method string, body any) (string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff(attempt)):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		ts, retryable, err := c.attempt(ctx, method, b)
		if err == nil {
			return ts, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}

	return "", lastErr
}

func (c *Client) attempt(ctx context.Context, method string, body []byte) (ts string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", true, err
	}
	defer func() { _ = resp.Body.Close() }()

	var out apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", true, fmt.Errorf("decode response: %w", err)
	}

	if out.OK {
		return out.TS, false, nil
	}

	err = fmt.Errorf("slack api error: %s", out.Error)
	return "", isRetryable(resp.StatusCode, out.Error), err
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
