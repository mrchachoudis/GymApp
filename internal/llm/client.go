// Package llm talks to the OpenCode Go endpoint.
//
// Two things here are load-bearing and were missing from the original sketch:
// a client timeout (http.DefaultClient has none, so a hung request hangs the
// handler forever) and reading the response body on a non-2xx (otherwise a
// 400 tells you "status 400" and nothing about what you sent wrong).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const DefaultEndpoint = "https://opencode.ai/zen/go/v1/chat/completions"

// Model IDs. The original doc listed these with an "opencode-go/" prefix in
// prose and without it in code; one of those is a 404. They are constants here
// so there is exactly one place to fix if the prefix turns out to be required.
const (
	ModelParser = "mimo-v2.5"
	ModelCoach  = "glm-5.2"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Client is the interface the pipeline depends on. Stub satisfies it too, so
// the whole app runs end to end without an API key or a single token spent.
type Client interface {
	Complete(ctx context.Context, req CompletionRequest) (string, error)
}

type CompletionRequest struct {
	Model       string
	System      string
	User        string
	Temperature float64
	MaxTokens   int
	// JSONMode asks the endpoint for guaranteed-parseable JSON. When the
	// provider honours it, stripFences becomes a no-op belt to the braces.
	JSONMode bool
}

type HTTPClient struct {
	Endpoint string
	APIKey   string
	HTTP     *http.Client
	// MaxRetries bounds transport-level retries (429 and 5xx only).
	MaxRetries int
}

func NewHTTPClient(apiKey, endpoint string) *HTTPClient {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &HTTPClient{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		HTTP:       &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 3,
	}
}

// StatusError carries the response body, which is where providers actually
// explain what was wrong with the request.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	body := e.Body
	if len(body) > 500 {
		body = body[:500] + "..."
	}
	return fmt.Sprintf("opencode: status %d: %s", e.Code, body)
}

// Retryable reports whether another attempt could plausibly succeed. A 400 is
// the caller's fault and will fail identically forever; a 429 or 503 will not.
func (e *StatusError) Retryable() bool {
	return e.Code == http.StatusTooManyRequests || e.Code >= 500
}

func (c *HTTPClient) Complete(ctx context.Context, r CompletionRequest) (string, error) {
	payload := request{
		Model:       r.Model,
		Messages:    []Message{{Role: "system", Content: r.System}, {Role: "user", Content: r.User}},
		Temperature: r.Temperature,
		MaxTokens:   r.MaxTokens,
	}
	if r.JSONMode {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff with jitter, so a rate limit hit by several
			// concurrent logs does not produce a synchronized retry stampede.
			delay := time.Duration(1<<uint(attempt-1)) * time.Second
			delay += time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		out, err := c.once(ctx, body)
		if err == nil {
			return out, nil
		}
		lastErr = err

		var se *StatusError
		if errors.As(err, &se) && se.Retryable() {
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		// Network-level errors are worth one more shot; anything else is not.
		if attempt == 0 && !errors.As(err, &se) {
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("after %d attempts: %w", c.MaxRetries+1, lastErr)
}

func (c *HTTPClient) once(ctx context.Context, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("opencode request: %w", err)
	}
	defer res.Body.Close()

	raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", &StatusError{Code: res.StatusCode, Body: string(raw)}
	}
	if readErr != nil {
		return "", fmt.Errorf("read body: %w", readErr)
	}

	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("opencode: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("opencode: empty response")
	}
	return out.Choices[0].Message.Content, nil
}

// StripFences removes markdown code fences from a model's JSON reply.
//
// Open models fence JSON roughly a third of the time no matter how loudly the
// prompt says not to, so this is not optional. It also trims any prose before
// the first brace and after the last, which is the other common failure.
func StripFences(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}

	// Fall back to brace matching for the "Here is the JSON:" preamble case.
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	return strings.TrimSpace(s)
}
