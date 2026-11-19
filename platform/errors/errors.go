package errors

import "errors"

var (
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("resource conflict")
	ErrInternal            = errors.New("internal server error")
	ErrBadRequest          = errors.New("bad request")
	ErrRateLimitExceeded   = errors.New("rate limit exceeded")
	ErrCircuitOpen         = errors.New("circuit breaker open: service unavailable")
	ErrBadGateway          = errors.New("bad gateway")
	ErrGatewayTimeout      = errors.New("gateway timeout")
	ErrTokenReplayDetected = errors.New("token replay detected: session compromised")
)
