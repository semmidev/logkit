package httpmw

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/event"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/secure"
)

// errorResponse is the wire format sent to clients: a stable code plus a
// safe message. Deliberately does NOT have a field for internal detail --
// there is no field to forget to omit.
type errorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// RespondError is the "crossing public boundaries" step from secure
// error handling: it logs the full internal error (via the request's
// wide event, if present, else directly through logger) and writes only
// the safe, generic representation to the client.
//
// err does not need to be a *secure.SafeError -- anything unrecognized
// is translated to the generic secure.CodeInternal response, which is
// the required fail-safe default: if we don't know an error is safe to
// describe, we assume it isn't.
func RespondError(ctx context.Context, w http.ResponseWriter, logger *logkit.Logger, err error) {
	se, ok := secure.As(err)
	if !ok {
		se = secure.Internal(err)
	}

	if ev, has := event.FromContext(ctx); has {
		ev.SetError(se)
	} else if logger != nil {
		logger.Error(ctx, "request failed", "error", se)
	}

	status := statusForCode(se.Code)
	body := errorResponse{
		Code:      se.Code,
		Message:   se.UserMsg,
		RequestID: logctx.RequestID(ctx),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// statusForCode maps SafeError's stable codes to HTTP statuses. Add your
// own domain codes to a similar switch in your own handler layer (or
// wrap this function) if you need statuses this default set doesn't
// cover -- it deliberately only knows about the generic codes defined in
// the secure package.
func statusForCode(code string) int {
	switch code {
	case secure.CodeNotFound:
		return http.StatusNotFound
	case secure.CodeInvalidInput:
		return http.StatusBadRequest
	case secure.CodeUnauthorized:
		return http.StatusUnauthorized
	case secure.CodeForbidden:
		return http.StatusForbidden
	case secure.CodeConflict:
		return http.StatusConflict
	case secure.CodeUnavailable:
		return http.StatusServiceUnavailable
	case secure.CodeTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}
