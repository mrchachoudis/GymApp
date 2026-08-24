// Package push delivers notifications to the phone through Firebase Cloud
// Messaging.
//
// A server cannot make an Android phone buzz on its own; it has to hand the
// message to Google, who owns the persistent connection to the device. FCM's
// HTTP v1 API needs an OAuth2 access token minted from a service-account key,
// which is why this package wants a JSON key file rather than a simple secret.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
)

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// Sender is the interface the rest of the app depends on, so the scheduler can
// be tested without a Firebase project.
type Sender interface {
	Send(ctx context.Context, tokens []string, n Message) (Result, error)
}

type Message struct {
	Title string
	Body  string
	// Data rides along silently; the Android app reads it to decide which
	// screen to open when the notification is tapped.
	Data map[string]string
}

type Result struct {
	Sent int
	// Stale are device tokens Firebase rejected as no longer valid. The caller
	// should delete them, otherwise every future send wastes a request on a
	// phone that was reinstalled months ago.
	Stale []string
}

type FCM struct {
	ProjectID string
	http      *http.Client
	// creds carries a TokenSource that refreshes the access token on its own,
	// so nothing here has to track expiry.
	creds *google.Credentials
}

// NewFCM builds a sender from a service-account JSON key. Get the file from
// the Firebase console under Project settings, Service accounts, Generate new
// private key. It is a credential: keep it on the mini PC, never in the APK.
func NewFCM(ctx context.Context, credentialsPath string) (*FCM, error) {
	raw, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read fcm credentials: %w", err)
	}
	creds, err := google.CredentialsFromJSON(ctx, raw, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("parse fcm credentials: %w", err)
	}

	var meta struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("read project_id: %w", err)
	}
	if meta.ProjectID == "" {
		return nil, errors.New("fcm credentials have no project_id")
	}

	return &FCM{
		ProjectID: meta.ProjectID,
		creds:     creds,
		http:      &http.Client{Timeout: 20 * time.Second},
	}, nil
}

type fcmRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification *fcmNotification  `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	Android      *androidConfig    `json:"android,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type androidConfig struct {
	Priority     string              `json:"priority,omitempty"`
	Notification *androidNotifConfig `json:"notification,omitempty"`
}

type androidNotifConfig struct {
	ChannelID string `json:"channel_id,omitempty"`
	Icon      string `json:"icon,omitempty"`
}

// Send delivers a message to every registered device.
//
// FCM v1 has no multicast endpoint, so this loops. With one user and one or
// two phones that is fine; batching would be premature.
func (f *FCM) Send(ctx context.Context, tokens []string, m Message) (Result, error) {
	var res Result
	if len(tokens) == 0 {
		return res, nil
	}

	tok, err := f.creds.TokenSource.Token()
	if err != nil {
		return res, fmt.Errorf("mint fcm access token: %w", err)
	}

	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", f.ProjectID)

	var firstErr error
	for _, deviceToken := range tokens {
		body, err := json.Marshal(fcmRequest{Message: fcmMessage{
			Token:        deviceToken,
			Notification: &fcmNotification{Title: m.Title, Body: m.Body},
			Data:         m.Data,
			Android: &androidConfig{
				Priority:     "high",
				Notification: &androidNotifConfig{ChannelID: "gym_reminders"},
			},
		}})
		if err != nil {
			return res, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return res, err
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := f.http.Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusOK:
			res.Sent++
		case resp.StatusCode == http.StatusNotFound ||
			(resp.StatusCode == http.StatusBadRequest && strings.Contains(string(raw), "INVALID_ARGUMENT")):
			// The phone was reinstalled or the app was removed. Report the
			// token so the caller can drop it.
			res.Stale = append(res.Stale, deviceToken)
		default:
			if firstErr == nil {
				firstErr = fmt.Errorf("fcm status %d: %s", resp.StatusCode, truncate(string(raw), 300))
			}
		}
	}
	return res, firstErr
}

// Noop is a Sender that logs nothing and sends nothing. It lets the whole
// service run before a Firebase project exists.
type Noop struct {
	Last []Message
}

func (n *Noop) Send(_ context.Context, tokens []string, m Message) (Result, error) {
	n.Last = append(n.Last, m)
	return Result{Sent: len(tokens)}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
