package controller

import "errors"

// Sentinel errors for specific failure conditions.
// These allow callers to check error types with errors.Is().
var (
	// ErrSecretNotFound indicates the credentials secret does not exist
	ErrSecretNotFound = errors.New("credentials secret not found")

	// ErrSecretMissingKey indicates the credentials secret is missing the api-key
	ErrSecretMissingKey = errors.New("credentials secret missing api-key")

	// ErrInvalidURL indicates the TrueNAS URL format is invalid
	ErrInvalidURL = errors.New("invalid TrueNAS URL format")
)

// IsConfigurationError returns true if the error is a permanent configuration
// problem that won't be resolved by retrying (e.g., invalid URL, missing secret key).
// These should be wrapped with reconcile.TerminalError().
func IsConfigurationError(err error) bool {
	return errors.Is(err, ErrSecretMissingKey) || errors.Is(err, ErrInvalidURL)
}
