package mail

import (
	"context"
	"log"
)

// Mailer defines transactional email dispatching.
type Mailer interface {
	SendVerificationCode(ctx context.Context, email, code string) error
	SendPasswordResetCode(ctx context.Context, email, code string) error
}

// LogMailer simulates email dispatch by logging to standard output.
type LogMailer struct{}

// NewLogMailer creates a new LogMailer.
func NewLogMailer() *LogMailer {
	return &LogMailer{}
}

// SendVerificationCode logs the registration verification email.
func (m *LogMailer) SendVerificationCode(ctx context.Context, email, code string) error {
	log.Printf("\n[SIMULATED EMAIL] ✉️ Sender: noreply@apigateway.com | Recipient: %s\n"+
		"Subject: Verify your Account\n"+
		"Body: Welcome to API Gateway & Auth Platform! Your verification OTP code is: %s\n"+
		"Note: This code expires in 5 minutes.\n", email, code)
	return nil
}

// SendPasswordResetCode logs the password reset email.
func (m *LogMailer) SendPasswordResetCode(ctx context.Context, email, code string) error {
	log.Printf("\n[SIMULATED EMAIL] ✉️ Sender: security@apigateway.com | Recipient: %s\n"+
		"Subject: Reset your Password\n"+
		"Body: A password reset request was received. Your reset OTP code is: %s\n"+
		"Note: This code expires in 5 minutes.\n", email, code)
	return nil
}
