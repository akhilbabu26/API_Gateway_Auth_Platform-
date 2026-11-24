package circuitbreaker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
)

// Decision represents the classifier's verdict on an HTTP request outcome.
type Decision uint8

const (
	// DecisionSuccess indicates the upstream outcome is considered healthy.
	DecisionSuccess Decision = iota
	// DecisionFailure indicates the upstream encountered a server or transport failure.
	DecisionFailure
	// DecisionAbandon indicates the call was aborted by client cancellation or client timeout,
	// so upstream health should not be penalized.
	DecisionAbandon
)

// Classifier evaluates whether an HTTP response or error constitutes an upstream failure,
// success, or abandonment.
type Classifier interface {
	Classify(req *http.Request, resp *http.Response, err error) Decision
}

// DefaultClassifier implements standard HTTP reverse-proxy classification rules.
type DefaultClassifier struct{}

// Classify evaluates the request, response, and error.
func (c DefaultClassifier) Classify(req *http.Request, resp *http.Response, err error) Decision {
	// 1. Error Analysis
	if err != nil {
		// Check for client-side cancellation or client-side deadline expiration
		if req != nil && req.Context() != nil {
			ctxErr := req.Context().Err()
			if errors.Is(ctxErr, context.Canceled) {
				// Client disconnected or canceled the request. NOT an upstream failure.
				return DecisionAbandon
			}
			if errors.Is(ctxErr, context.DeadlineExceeded) && !isUpstreamTimeout(err) {
				// Inbound client deadline expired before upstream responded. NOT an upstream failure.
				return DecisionAbandon
			}
		}

		// Check for genuine upstream transport timeouts
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return DecisionFailure
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return DecisionFailure
		}

		// Check for transport connection errors
		if errors.Is(err, syscall.ECONNREFUSED) ||
			errors.Is(err, syscall.ECONNRESET) ||
			errors.Is(err, io.ErrUnexpectedEOF) ||
			errors.Is(err, io.EOF) {
			return DecisionFailure
		}

		return DecisionFailure
	}

	// 2. HTTP Status Code Evaluation
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable, // 503
			http.StatusGatewayTimeout:     // 504
			return DecisionFailure
		case http.StatusTooManyRequests: // 429
			// Rate limiting by upstream is normal backpressure, not server failure
			return DecisionSuccess
		default:
			// 2xx, 3xx, and 4xx (400, 401, 403, 404, etc.) are considered healthy upstream responses
			return DecisionSuccess
		}
	}

	return DecisionSuccess
}

func isUpstreamTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
