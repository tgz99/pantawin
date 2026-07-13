package monitor

import (
	"math/rand"
	"testing"
	"testing/quick"
)

// Spec 7.2 item 1: UP→DOWN only after N consecutive fails; DOWN→UP on first
// success; PAUSED ignores checks. PENDING resolves to UP on first success or
// DOWN after threshold consecutive fails.

func TestApply_UpStaysUpOnSuccess(t *testing.T) {
	next, transitioned := Apply(StatusUp, 0, true, 2)
	if next.Status != StatusUp || transitioned {
		t.Errorf("UP + success => expected (UP, no transition), got (%s, %v)", next.Status, transitioned)
	}
	if next.ConsecutiveFailures != 0 {
		t.Errorf("expected failure counter reset to 0, got %d", next.ConsecutiveFailures)
	}
}

func TestApply_UpSingleFailureBelowThresholdStaysUp(t *testing.T) {
	next, transitioned := Apply(StatusUp, 0, false, 2)
	if next.Status != StatusUp || transitioned {
		t.Errorf("UP + 1st failure (threshold 2) => expected (UP, no transition), got (%s, %v)", next.Status, transitioned)
	}
	if next.ConsecutiveFailures != 1 {
		t.Errorf("expected failure counter 1, got %d", next.ConsecutiveFailures)
	}
}

func TestApply_UpToDownAtThreshold(t *testing.T) {
	next, transitioned := Apply(StatusUp, 1, false, 2)
	if next.Status != StatusDown || !transitioned {
		t.Errorf("UP + 2nd consecutive failure (threshold 2) => expected (DOWN, transitioned), got (%s, %v)", next.Status, transitioned)
	}
}

func TestApply_DownToUpOnFirstSuccess(t *testing.T) {
	next, transitioned := Apply(StatusDown, 5, true, 2)
	if next.Status != StatusUp || !transitioned {
		t.Errorf("DOWN + success => expected (UP, transitioned), got (%s, %v)", next.Status, transitioned)
	}
	if next.ConsecutiveFailures != 0 {
		t.Errorf("expected failure counter reset, got %d", next.ConsecutiveFailures)
	}
}

func TestApply_DownStaysDownOnFailure(t *testing.T) {
	next, transitioned := Apply(StatusDown, 5, false, 2)
	if next.Status != StatusDown || transitioned {
		t.Errorf("DOWN + failure => expected (DOWN, no transition), got (%s, %v)", next.Status, transitioned)
	}
}

func TestApply_PendingToUpOnFirstSuccess(t *testing.T) {
	next, transitioned := Apply(StatusPending, 0, true, 2)
	if next.Status != StatusUp || !transitioned {
		t.Errorf("PENDING + success => expected (UP, transitioned), got (%s, %v)", next.Status, transitioned)
	}
}

func TestApply_PendingRequiresThresholdToGoDown(t *testing.T) {
	next, transitioned := Apply(StatusPending, 0, false, 3)
	if next.Status != StatusPending || transitioned {
		t.Errorf("PENDING + 1st failure (threshold 3) => expected (PENDING, no transition), got (%s, %v)", next.Status, transitioned)
	}
	next, transitioned = Apply(StatusPending, 2, false, 3)
	if next.Status != StatusDown || !transitioned {
		t.Errorf("PENDING + 3rd consecutive failure (threshold 3) => expected (DOWN, transitioned), got (%s, %v)", next.Status, transitioned)
	}
}

func TestApply_PausedIgnoresChecksEntirely(t *testing.T) {
	for _, ok := range []bool{true, false} {
		next, transitioned := Apply(StatusPaused, 0, ok, 2)
		if next.Status != StatusPaused || transitioned {
			t.Errorf("PAUSED + check(ok=%v) => expected (PAUSED, no transition), got (%s, %v)", ok, next.Status, transitioned)
		}
	}
}

func TestApply_ThresholdOneIsImmediate(t *testing.T) {
	next, transitioned := Apply(StatusUp, 0, false, 1)
	if next.Status != StatusDown || !transitioned {
		t.Errorf("threshold 1: UP + failure => expected (DOWN, transitioned), got (%s, %v)", next.Status, transitioned)
	}
}

// Property-based checks over random sequences (spec 7.2: "Property-based
// tests on random check sequences").

// Property: from any state, a DOWN status can only be reached when the
// failure counter has actually accumulated `threshold` consecutive fails.
func TestProperty_DownOnlyAfterThresholdConsecutiveFails(t *testing.T) {
	f := func(seed int64, thresholdRaw uint8) bool {
		threshold := int(thresholdRaw%5) + 1 // 1..5
		rng := rand.New(rand.NewSource(seed))

		state := State{Status: StatusPending, ConsecutiveFailures: 0}
		consecutiveFails := 0
		for i := 0; i < 200; i++ {
			ok := rng.Intn(3) != 0 // ~2/3 success
			var transitioned bool
			prev := state.Status
			state, transitioned = Apply(state.Status, state.ConsecutiveFailures, ok, threshold)

			if ok {
				consecutiveFails = 0
			} else {
				consecutiveFails++
			}

			// If we just transitioned into DOWN, the model must agree that
			// at least `threshold` consecutive failures have occurred.
			if transitioned && state.Status == StatusDown && consecutiveFails < threshold {
				t.Logf("entered DOWN after only %d consecutive fails (threshold %d, prev %s)", consecutiveFails, threshold, prev)
				return false
			}
			// DOWN -> UP must happen on the very first success.
			if prev == StatusDown && ok && state.Status != StatusUp {
				t.Logf("DOWN + success did not recover to UP")
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Error(err)
	}
}

// Property: the failure counter is always in [0, threshold] and resets on success.
func TestProperty_CounterBoundsAndReset(t *testing.T) {
	f := func(seed int64, thresholdRaw uint8) bool {
		threshold := int(thresholdRaw%5) + 1
		rng := rand.New(rand.NewSource(seed))

		state := State{Status: StatusPending}
		for i := 0; i < 200; i++ {
			ok := rng.Intn(2) == 0
			state, _ = Apply(state.Status, state.ConsecutiveFailures, ok, threshold)
			if ok && state.ConsecutiveFailures != 0 {
				return false
			}
			if state.ConsecutiveFailures < 0 || state.ConsecutiveFailures > threshold {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Error(err)
	}
}
