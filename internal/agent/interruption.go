package agent

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// classifyInterruption turns the error that ended a turn into the record a
// resume decides from.
//
// The question it answers is narrower than "what went wrong": it is "would
// running this again, later, plausibly get further?". A rate limit says yes
// once the window rolls over; a rejected key says yes once a human fixes the
// account; a malformed request says no, because the same request will be
// malformed the next time too. Offering a Resume button on that last case
// would be worse than offering nothing — it invites the user to spend their
// time re-running a failure.
//
// Everything unrecognised is treated as resumable. The cost of a resume that
// fails again is one wasted request; the cost of refusing to resume something
// that would have worked is a session the user cannot get back.
func classifyInterruption(err error, step int) *session.Interruption {
	if err == nil {
		return nil
	}
	at := func(d time.Duration) int64 {
		if d <= 0 {
			return 0
		}
		return time.Now().Add(d).Unix()
	}

	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusTooManyRequests:
			return &session.Interruption{
				Reason:     session.InterruptRateLimit,
				Resumable:  true,
				Detail:     rateLimitDetail(apiErr.RetryAfter),
				RetryAfter: at(apiErr.RetryAfter),
				Step:       step,
			}
		case apiErr.IsContextLength():
			return &session.Interruption{
				Reason:    session.InterruptContext,
				Resumable: true,
				Detail:    "The request outgrew the model's context window and compaction could not bring it back under. Resuming will compact again; a model with a larger window would hold more.",
				Step:      step,
			}
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusPaymentRequired:
			return &session.Interruption{
				Reason:    session.InterruptAuth,
				Resumable: true,
				Detail:    "The provider rejected the credentials or the account has no balance left. Fix that, then resume — the conversation is intact.",
				Step:      step,
			}
		case apiErr.StatusCode >= 500:
			return &session.Interruption{
				Reason:    session.InterruptServerError,
				Resumable: true,
				Detail:    "The provider failed on its own side. Resuming retries the same request.",
				Step:      step,
			}
		case apiErr.StatusCode >= 400:
			// A 4xx that is none of the above is the request itself being wrong,
			// and the resumed request would be identically wrong.
			return &session.Interruption{
				Reason:    session.InterruptFatal,
				Resumable: false,
				Detail:    "The provider rejected the request itself. Resuming would send the same one — change the model or the prompt instead.",
				Step:      step,
			}
		}
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "429") || strings.Contains(lower, "quota"):
		return &session.Interruption{
			Reason:    session.InterruptRateLimit,
			Resumable: true,
			Detail:    rateLimitDetail(0),
			Step:      step,
		}
	case provider.IsContextLengthMessage(err.Error()):
		return &session.Interruption{
			Reason:    session.InterruptContext,
			Resumable: true,
			Detail:    "The request outgrew the model's context window. Resuming will compact again.",
			Step:      step,
		}
	case strings.Contains(lower, "connection reset") || strings.Contains(lower, "eof") ||
		strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "refused") || strings.Contains(lower, "no such host"):
		return &session.Interruption{
			Reason:    session.InterruptNetwork,
			Resumable: true,
			Detail:    "The connection to the provider dropped. Resuming retries it.",
			Step:      step,
		}
	}

	return &session.Interruption{
		Reason:    session.InterruptServerError,
		Resumable: true,
		Detail:    "The turn ended on an error the loop could not recover from. Resuming retries the step it died on.",
		Step:      step,
	}
}

// rateLimitDetail words the wait, naming the time when the provider gave one.
// "Try again later" is what every rate-limit message already says; the useful
// version says when later is.
func rateLimitDetail(retryAfter time.Duration) string {
	if retryAfter <= 0 {
		return "The provider's rate limit or session quota is exhausted. Resume once the window rolls over."
	}
	return "The provider's rate limit or session quota is exhausted, and it asked to be left alone for " +
		retryAfter.Round(time.Second).String() + ". Resume after that."
}

// crashedInterruption is the record for a turn found unfinished at startup. The
// process died before it could write down what happened, so the reason is the
// absence of one.
func crashedInterruption() *session.Interruption {
	return &session.Interruption{
		Reason:    session.InterruptCrashed,
		Resumable: true,
		Detail:    "The server stopped while this turn was still running, so it was never finished. The conversation up to that point is intact.",
	}
}

// strandedInterruption is the record for a turn that did write a finish reason,
// just not one the model chose.
//
// The commonest shape by far: the model asked for a tool, the tool ran, and the
// loop ended before pairing the result — leaving a session that cannot be
// continued at all until the pairing is written, because the provider rejects a
// tool call nothing answers. Sessions in that state look merely idle from the
// outside, which is why they sit there.
func strandedInterruption(finish *string) *session.Interruption {
	detail := "This turn stopped before the model finished it, and no loop is running. The conversation up to that point is intact."
	if finish != nil {
		switch *finish {
		case "tool_calls":
			detail = "The model asked to run a tool and the loop ended before the turn was finished. Resuming carries on from the results it did get."
		case "length", "max_tokens":
			detail = "The model hit its output limit mid-answer. Resuming lets it carry on from where it stopped."
		case "error":
			detail = "This turn ended on an error. The conversation up to that point is intact — resuming retries the step it stopped on."
		}
	}
	return &session.Interruption{
		Reason:    session.InterruptStalled,
		Resumable: true,
		Detail:    detail,
	}
}
