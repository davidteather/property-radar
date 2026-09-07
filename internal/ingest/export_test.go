package ingest

import "time"

// Test hooks for the external ingest_test package.
var FinishRetryWait = &finishRetryWait

const FinishAttempts = finishAttempts

func SetNow(s *Service, now func() time.Time) { s.now = now }
