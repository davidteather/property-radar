package streeteasy

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUnsupportedListingType = errors.New("unsupported listing type")
	ErrInvalidArea            = errors.New("invalid area identifier")
	ErrNoAreas                = errors.New("no areas requested")
	ErrListingNotFound        = errors.New("listing payload not found in page")
)

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("unexpected http status %d", e.StatusCode)
	}
	return fmt.Sprintf("unexpected http status %d: %s", e.StatusCode, e.Body)
}

// retryable covers transient throttling and the 403 StreetEasy returns on a
// rate-limited IP; a rotating proxy usually clears it next attempt.
func (e *HTTPError) retryable() bool {
	return e.StatusCode == 403 || e.StatusCode == 408 || e.StatusCode == 429 || e.StatusCode >= 500
}

type GraphQLError struct {
	Messages []string
}

func (e *GraphQLError) Error() string {
	return "graphql: " + strings.Join(e.Messages, "; ")
}
