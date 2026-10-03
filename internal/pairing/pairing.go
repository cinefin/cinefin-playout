// Package pairing issues the short code a person reads off the player's screen
// and types into Cinefin to pair it.
//
// The code is six digits and short-lived: it rotates every CodeLifetime, after
// every wrong attempt, and once used. Attempts are limited to one a second, and
// maxFailures wrong codes in a row lock pairing for a minute, so guessing is
// impractical even though the pairing endpoint is open to the LAN.
package pairing

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

const (
	// CodeLifetime is how long a code stays valid before it rotates.
	CodeLifetime = 5 * time.Minute

	maxFailures = 5
	lockout     = time.Minute
	minInterval = time.Second
)

var (
	ErrWrongCode = errors.New("wrong pairing code")
	ErrLocked    = errors.New("too many wrong codes, try again in a minute")
	ErrTooFast   = errors.New("too many attempts, try again in a second")
)

// Codes holds the current pairing code. It is safe for concurrent use.
type Codes struct {
	now      func() time.Time
	onRotate func(code string)

	mu          sync.Mutex
	code        string
	expires     time.Time
	failures    int
	lockedUntil time.Time
	lastTry     time.Time
}

// New returns a Codes with a fresh code.
func New() *Codes {
	c := &Codes{now: time.Now}
	c.rotateLocked()
	return c
}

// OnRotate registers fn, called with each new code (not the first one). It runs
// outside the lock and may call Current.
func (c *Codes) OnRotate(fn func(code string)) { c.onRotate = fn }

// Current returns the valid code and when it expires, rotating it first if it
// has expired.
func (c *Codes) Current() (code string, expires time.Time) {
	c.mu.Lock()
	rotated := c.expireLocked()
	code, expires = c.code, c.expires
	c.mu.Unlock()
	if rotated {
		c.notify(code)
	}
	return code, expires
}

// Check tests an entered code. Spaces and dashes are ignored. On success the
// code is used up and nil is returned; it is replaced without notifying, since
// the player is about to be paired and has no code to show. A wrong code
// rotates it (and notifies).
func (c *Codes) Check(input string) error {
	c.mu.Lock()
	now := c.now()
	if now.Before(c.lockedUntil) {
		c.mu.Unlock()
		return ErrLocked
	}
	if now.Sub(c.lastTry) < minInterval {
		c.mu.Unlock()
		return ErrTooFast
	}
	c.lastTry = now
	c.expireLocked()

	ok := subtle.ConstantTimeCompare([]byte(normalize(input)), []byte(c.code)) == 1
	var err error
	if ok {
		c.failures = 0
	} else {
		err = ErrWrongCode
		c.failures++
		if c.failures >= maxFailures {
			c.failures = 0
			c.lockedUntil = now.Add(lockout)
		}
	}
	code := c.rotateLocked()
	c.mu.Unlock()
	if !ok {
		c.notify(code)
	}
	return err
}

// Rotate replaces the code now and notifies, for a player that was just
// unpaired and should show a fresh code.
func (c *Codes) Rotate() {
	c.mu.Lock()
	code := c.rotateLocked()
	c.mu.Unlock()
	c.notify(code)
}

// Run rotates the code when it expires, so OnRotate fires (and the screen and
// log update) even when nobody asks for the code. It returns when ctx ends.
func (c *Codes) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Current()
		}
	}
}

// Format groups a code for reading aloud and display: "482913" -> "482 913".
func Format(code string) string {
	if len(code) != 6 {
		return code
	}
	return code[:3] + " " + code[3:]
}

func (c *Codes) notify(code string) {
	if c.onRotate != nil {
		c.onRotate(code)
	}
}

// expireLocked rotates an expired code and reports whether it did.
func (c *Codes) expireLocked() bool {
	if c.now().Before(c.expires) {
		return false
	}
	c.rotateLocked()
	return true
}

func (c *Codes) rotateLocked() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		// crypto/rand does not fail on supported platforms; if it ever does,
		// refuse every attempt rather than issue a predictable code.
		c.code = "x"
	} else {
		c.code = fmt.Sprintf("%06d", n.Int64())
	}
	c.expires = c.now().Add(CodeLifetime)
	return c.code
}

func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
