package ratelimit

import (
	"testing"
	"time"
)

func TestAllowsUpToLimitThenBlocks(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := NewWithClock(Limits{Minute: 2}, func() time.Time { return now })

	if allowed, _ := limiter.Allow("1.2.3.4"); !allowed {
		t.Fatal("first request should pass")
	}
	if allowed, _ := limiter.Allow("1.2.3.4"); !allowed {
		t.Fatal("second request should pass")
	}
	allowed, retry := limiter.Allow("1.2.3.4")
	if allowed {
		t.Fatal("third request should be blocked")
	}
	if retry <= 0 || retry > time.Minute {
		t.Fatalf("retry = %v, want 0 < retry <= 1m", retry)
	}

	// A different key has its own budget.
	if allowed, _ := limiter.Allow("5.6.7.8"); !allowed {
		t.Fatal("other keys must not be affected")
	}
}

func TestWindowResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := NewWithClock(Limits{Minute: 1}, func() time.Time { return now })

	if allowed, _ := limiter.Allow("k"); !allowed {
		t.Fatal("first should pass")
	}
	if allowed, _ := limiter.Allow("k"); allowed {
		t.Fatal("second should block")
	}

	now = now.Add(61 * time.Second)
	if allowed, _ := limiter.Allow("k"); !allowed {
		t.Fatal("should pass in the next window")
	}
}

func TestDisabledWindowIsIgnored(t *testing.T) {
	limiter := NewWithClock(Limits{Minute: 0, Hour: 0, Day: 0}, time.Now)
	for i := 0; i < 100; i++ {
		if allowed, _ := limiter.Allow("k"); !allowed {
			t.Fatal("disabled limits must always allow")
		}
	}
}
