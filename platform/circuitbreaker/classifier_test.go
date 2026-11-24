package circuitbreaker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"
)

type customTimeoutError struct{}

func (customTimeoutError) Error() string   { return "custom network timeout" }
func (customTimeoutError) Timeout() bool   { return true }
func (customTimeoutError) Temporary() bool { return true }

func TestDefaultClassifierHTTPStatus(t *testing.T) {
	classifier := DefaultClassifier{}
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)

	tests := []struct {
		code int
		want Decision
	}{
		{http.StatusOK, DecisionSuccess},
		{http.StatusCreated, DecisionSuccess},
		{http.StatusNoContent, DecisionSuccess},
		{http.StatusBadRequest, DecisionSuccess},
		{http.StatusUnauthorized, DecisionSuccess},
		{http.StatusForbidden, DecisionSuccess},
		{http.StatusNotFound, DecisionSuccess},
		{http.StatusTooManyRequests, DecisionSuccess}, // 429
		{http.StatusInternalServerError, DecisionFailure}, // 500
		{http.StatusBadGateway, DecisionFailure},          // 502
		{http.StatusServiceUnavailable, DecisionFailure}, // 503
		{http.StatusGatewayTimeout, DecisionFailure},     // 504
	}

	for _, tt := range tests {
		resp := &http.Response{StatusCode: tt.code}
		got := classifier.Classify(req, resp, nil)
		if got != tt.want {
			t.Errorf("status %d: got Decision %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestDefaultClassifierErrors(t *testing.T) {
	classifier := DefaultClassifier{}

	t.Run("client cancellation triggers abandon", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // immediately canceled
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil).WithContext(ctx)

		got := classifier.Classify(req, nil, context.Canceled)
		if got != DecisionAbandon {
			t.Fatalf("got %v, want DecisionAbandon", got)
		}
	})

	t.Run("client deadline exceeded triggers abandon", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()
		time.Sleep(5 * time.Millisecond)
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil).WithContext(ctx)

		got := classifier.Classify(req, nil, errors.New("client context canceled"))
		if got != DecisionAbandon {
			t.Fatalf("got %v, want DecisionAbandon", got)
		}
	})

	t.Run("upstream network timeout triggers failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		got := classifier.Classify(req, nil, customTimeoutError{})
		if got != DecisionFailure {
			t.Fatalf("got %v, want DecisionFailure", got)
		}
	})

	t.Run("connection refused triggers failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		got := classifier.Classify(req, nil, syscall.ECONNREFUSED)
		if got != DecisionFailure {
			t.Fatalf("got %v, want DecisionFailure", got)
		}
	})

	t.Run("connection reset triggers failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		got := classifier.Classify(req, nil, syscall.ECONNRESET)
		if got != DecisionFailure {
			t.Fatalf("got %v, want DecisionFailure", got)
		}
	})

	t.Run("unexpected EOF triggers failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		got := classifier.Classify(req, nil, io.ErrUnexpectedEOF)
		if got != DecisionFailure {
			t.Fatalf("got %v, want DecisionFailure", got)
		}
	})
}
