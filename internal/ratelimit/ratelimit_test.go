package ratelimit

import (
	"testing"
	"time"
)

func TestSlidingWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(3, time.Hour)
	l.Now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, ok := l.Check("ip"); !ok {
			t.Fatalf("attempt %d should be allowed", i)
		}
		l.Record("ip")
		now = now.Add(10 * time.Minute)
	}
	retry, ok := l.Check("ip")
	if ok || retry != 30*time.Minute {
		t.Fatalf("4th attempt: ok=%v retry=%v, want blocked for 30m", ok, retry)
	}
	if _, ok := l.Check("other-ip"); !ok {
		t.Fatal("other keys are not affected")
	}

	now = now.Add(30 * time.Minute) // the first attempt expires
	if _, ok := l.Check("ip"); !ok {
		t.Fatal("should be allowed once the oldest attempt is an hour old")
	}

	now = now.Add(2 * time.Hour)
	l.Sweep()
	if len(l.hits) != 0 {
		t.Fatalf("sweep should drop expired keys, %d left", len(l.hits))
	}
}
