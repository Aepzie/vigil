package collector

import (
	"math/rand/v2"
	"time"
)

// backoff produces exponentially growing reconnect delays with jitter so
// many servers failing at once do not reconnect in lockstep. The zero
// value is not usable; use newBackoff.
type backoff struct {
	min, max time.Duration
	attempt  int
}

func newBackoff(minDelay, maxDelay time.Duration) backoff {
	return backoff{min: minDelay, max: maxDelay}
}

// next returns the delay before the next attempt: a random duration in
// [d/2, d] where d doubles per attempt from min up to max.
func (b *backoff) next() time.Duration {
	d := b.max
	if b.attempt < 32 {
		if shifted := b.min << b.attempt; shifted > 0 && shifted < b.max {
			d = shifted
		}
	}
	b.attempt++
	half := d / 2
	return half + rand.N(d-half+1)
}

// reset restarts the sequence after a successful connection.
func (b *backoff) reset() { b.attempt = 0 }
