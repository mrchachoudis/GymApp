package api

import (
	"context"
	"net/http"
	"time"
)

// contextWithTimeout derives a bounded context from the request. The request's
// own context still cancels it, so a phone that drops the connection mid-log
// releases the handler immediately instead of waiting out the LLM call.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
