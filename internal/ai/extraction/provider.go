package extraction

import "context"

// Provider turns raw report text into JSON that must be validated by Validator.
type Provider interface {
	Extract(context.Context, string) ([]byte, error)
}
