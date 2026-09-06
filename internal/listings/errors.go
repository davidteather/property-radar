package listings

import (
	"errors"
	"fmt"
)

// Application-level sentinels the Service returns and transports map to their own wire errors; a transport never imports internal/store. The concrete store aliases these, so the Service detects them with errors.Is and no import cycle is introduced.
var (
	ErrNotFound             = errors.New("listing not found")
	ErrFutureThroughVerdict = errors.New("rubric through_verdict_id is ahead of the latest verdict")
	ErrDuplicateCrawlTarget = errors.New("an identical crawl target is already pending")
	ErrQueueFull            = errors.New("the crawl request queue is full")
	ErrCrawlTargetNotFound  = errors.New("crawl request not found")
	ErrListNotFound         = errors.New("list not found")
	ErrDefaultList          = errors.New("the default favorites list cannot be deleted")
	ErrDuplicateList        = errors.New("a list with that name already exists")
	ErrInvalidInput         = errors.New("invalid input")
)

// inputError carries a caller-facing message and matches ErrInvalidInput, so transports can return it verbatim as a client error instead of a masked 500.
type inputError struct{ msg string }

func (e inputError) Error() string      { return e.msg }
func (inputError) Is(target error) bool { return target == ErrInvalidInput }

// Invalidf builds an ErrInvalidInput whose text is the formatted message itself.
func Invalidf(format string, args ...any) error {
	return inputError{msg: fmt.Sprintf(format, args...)}
}
