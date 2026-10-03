package pairing

import (
	"errors"
	"testing"
	"time"
)

// fakeClock lets tests move time forward without sleeping.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestCodes() (*Codes, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := &Codes{now: clk.now}
	c.rotateLocked()
	return c, clk
}

func TestCorrectCodeIsSingleUse(t *testing.T) {
	c, clk := newTestCodes()
	code, _ := c.Current()
	if len(code) != 6 {
		t.Fatalf("code %q is not six digits", code)
	}
	if err := c.Check(Format(code)); err != nil {
		t.Fatalf("Check(correct, formatted) = %v", err)
	}
	clk.advance(2 * time.Second)
	if err := c.Check(code); !errors.Is(err, ErrWrongCode) {
		t.Errorf("reusing a code = %v, want ErrWrongCode", err)
	}
}

func TestWrongCodeRotates(t *testing.T) {
	c, _ := newTestCodes()
	var rotated []string
	c.OnRotate(func(code string) { rotated = append(rotated, code) })
	before, _ := c.Current()
	wrong := "000000"
	if before == wrong {
		wrong = "111111"
	}
	if err := c.Check(wrong); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("Check(wrong) = %v", err)
	}
	after, _ := c.Current()
	if len(rotated) != 1 || rotated[0] != after {
		t.Errorf("OnRotate calls = %v, current %q", rotated, after)
	}
}

func TestAttemptLimits(t *testing.T) {
	c, clk := newTestCodes()
	if err := c.Check("x"); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("first attempt = %v", err)
	}
	if err := c.Check("x"); !errors.Is(err, ErrTooFast) {
		t.Errorf("attempt within a second = %v, want ErrTooFast", err)
	}
	for i := 1; i < maxFailures; i++ {
		clk.advance(minInterval)
		if err := c.Check("x"); !errors.Is(err, ErrWrongCode) {
			t.Fatalf("attempt %d = %v", i+1, err)
		}
	}
	clk.advance(minInterval)
	code, _ := c.Current()
	if err := c.Check(code); !errors.Is(err, ErrLocked) {
		t.Errorf("correct code while locked = %v, want ErrLocked", err)
	}
	clk.advance(lockout)
	code, _ = c.Current()
	if err := c.Check(code); err != nil {
		t.Errorf("correct code after lockout = %v", err)
	}
}

func TestExpiry(t *testing.T) {
	c, clk := newTestCodes()
	var rotations int
	c.OnRotate(func(string) { rotations++ })
	_, exp := c.Current()
	if !exp.Equal(clk.t.Add(CodeLifetime)) {
		t.Errorf("expires = %v", exp)
	}
	clk.advance(CodeLifetime)
	_, exp2 := c.Current()
	if rotations != 1 || !exp2.After(exp) {
		t.Errorf("expired code not rotated: rotations=%d expires %v -> %v", rotations, exp, exp2)
	}
}
