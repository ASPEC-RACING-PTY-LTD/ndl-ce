package backup

import "time"

// Tests run the upload queue with short retry pacing.
func init() {
	queueRetry = retryPolicy{base: 20 * time.Millisecond, max: 200 * time.Millisecond, attempts: 8, rearm: time.Hour, tick: 20 * time.Millisecond}
}
