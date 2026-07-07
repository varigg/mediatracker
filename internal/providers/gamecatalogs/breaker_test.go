package gamecatalogs

import "testing"

func TestBreakerTripsAtThreshold(t *testing.T) {
	b := newBreaker(3)
	for i := 0; i < 2; i++ {
		b.Failure()
		if !b.Allow() {
			t.Fatalf("breaker open after %d failures, threshold is 3", i+1)
		}
	}
	b.Failure()
	if b.Allow() {
		t.Fatal("breaker still closed after 3 consecutive failures")
	}
}

func TestBreakerSuccessResetsCount(t *testing.T) {
	b := newBreaker(3)
	b.Failure()
	b.Failure()
	b.Success()
	b.Failure()
	b.Failure()
	if !b.Allow() {
		t.Fatal("success must reset the consecutive-failure count")
	}
}

func TestBreakerResetClosesCircuit(t *testing.T) {
	b := newBreaker(1)
	b.Failure()
	if b.Allow() {
		t.Fatal("threshold-1 breaker must open on first failure")
	}
	b.Reset()
	if !b.Allow() {
		t.Fatal("Reset must close the circuit for the next cycle")
	}
}
