package ratelimit

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestBucketBurstThenDeny(t *testing.T) {
	b := NewBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		if !b.Allow(t0) {
			t.Fatalf("message %d denied within burst", i+1)
		}
	}
	if b.Allow(t0) {
		t.Fatal("message 61 allowed past burst")
	}
}

func TestBucketRefill(t *testing.T) {
	b := NewBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		b.Allow(t0)
	}
	// 100ms at 30/s = 3 tokens.
	now := t0.Add(100 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if !b.Allow(now) {
			t.Fatalf("refilled token %d denied", i+1)
		}
	}
	if b.Allow(now) {
		t.Fatal("allowed beyond refilled tokens")
	}
}

func TestBucketRefillCapsAtBurst(t *testing.T) {
	b := NewBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		b.Allow(t0)
	}
	now := t0.Add(time.Hour)
	for i := 0; i < 60; i++ {
		if !b.Allow(now) {
			t.Fatalf("message %d denied after long idle", i+1)
		}
	}
	if b.Allow(now) {
		t.Fatal("idle time banked more than burst")
	}
}

func TestBucketBackwardsClockIgnored(t *testing.T) {
	b := NewBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		b.Allow(t0)
	}
	if b.Allow(t0.Add(-time.Hour)) {
		t.Fatal("backwards clock granted tokens")
	}
}

func TestBucketZeroDisables(t *testing.T) {
	for _, b := range []*Bucket{NewBucket(0, 0, t0), NewBucket(0, 60, t0), NewBucket(30, 0, t0)} {
		for i := 0; i < 10000; i++ {
			if !b.Allow(t0) {
				t.Fatalf("disabled bucket %+v denied", b)
			}
		}
	}
}
