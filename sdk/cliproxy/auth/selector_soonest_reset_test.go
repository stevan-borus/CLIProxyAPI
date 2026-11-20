package auth

import (
	"context"
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func soonestResetUnix(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

func claudeQuotaAuth(id string, reset7d time.Time, observedAt time.Time) *Auth {
	return &Auth{
		ID: id,
		Quota: QuotaState{
			ObservedAt: observedAt,
			Signals: map[string]string{
				"Anthropic-Ratelimit-Unified-7d-Reset": soonestResetUnix(reset7d),
				"Anthropic-Ratelimit-Unified-5h-Reset": soonestResetUnix(observedAt.Add(time.Hour)),
			},
		},
	}
}

func pickSoonestReset(t *testing.T, provider, model string, auths ...*Auth) *Auth {
	t.Helper()
	got, err := (&SoonestResetSelector{}).Pick(context.Background(), provider, model, cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got == nil {
		t.Fatalf("Pick() auth = nil")
	}
	return got
}

func TestSoonestResetSelectorPick_SoonestWeeklyResetWins(t *testing.T) {
	t.Parallel()

	now := time.Now()
	later := claudeQuotaAuth("a", now.Add(5*24*time.Hour), now)
	sooner := claudeQuotaAuth("b", now.Add(2*24*time.Hour), now)

	if got := pickSoonestReset(t, "claude", "", later, sooner); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_CaseInsensitiveAndFallbackResetKey(t *testing.T) {
	t.Parallel()

	now := time.Now()
	later := claudeQuotaAuth("a", now.Add(5*24*time.Hour), now)
	fallback := &Auth{
		ID: "b",
		Quota: QuotaState{
			ObservedAt: now,
			Signals: map[string]string{
				"anthropic-ratelimit-unified-reset": soonestResetUnix(now.Add(24 * time.Hour)),
			},
		},
	}

	if got := pickSoonestReset(t, "claude", "", later, fallback); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_UnknownStaleAndMissingGoLast(t *testing.T) {
	t.Parallel()

	now := time.Now()
	noSignals := &Auth{ID: "a"}
	stale := claudeQuotaAuth("b", now.Add(-time.Hour), now.Add(-2*time.Hour))
	garbage := &Auth{
		ID: "c",
		Quota: QuotaState{
			ObservedAt: now,
			Signals:    map[string]string{"Anthropic-Ratelimit-Unified-7d-Reset": "not-a-number"},
		},
	}
	known := claudeQuotaAuth("z", now.Add(6*24*time.Hour), now)

	if got := pickSoonestReset(t, "claude", "", noSignals, stale, garbage, known); got.ID != "z" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "z")
	}
}

func TestSoonestResetSelectorPick_AllUnknownFallsBackToLowestID(t *testing.T) {
	t.Parallel()

	now := time.Now()
	stale := claudeQuotaAuth("a", now.Add(-time.Minute), now.Add(-time.Hour))

	if got := pickSoonestReset(t, "gemini", "", &Auth{ID: "c"}, &Auth{ID: "b"}, stale); got.ID != "a" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "a")
	}
	if got := pickSoonestReset(t, "gemini", "", &Auth{ID: "c"}, &Auth{ID: "b"}); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_TieBreaksByID(t *testing.T) {
	t.Parallel()

	now := time.Now()
	reset := now.Add(3 * 24 * time.Hour)

	if got := pickSoonestReset(t, "claude", "", claudeQuotaAuth("b", reset, now), claudeQuotaAuth("a", reset, now)); got.ID != "a" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "a")
	}
}

func TestSoonestResetSelectorPick_CodexUsesLargerWindow(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// Primary is the short window resetting very soon; secondary is the weekly
	// window. The weekly window must decide.
	shortPrimary := &Auth{
		ID: "a",
		Quota: QuotaState{
			ObservedAt: now,
			Signals: map[string]string{
				"X-Codex-Primary-Reset-At":         soonestResetUnix(now.Add(time.Hour)),
				"X-Codex-Primary-Window-Minutes":   "300",
				"X-Codex-Secondary-Reset-At":       soonestResetUnix(now.Add(6 * 24 * time.Hour)),
				"X-Codex-Secondary-Window-Minutes": "10080",
			},
		},
	}
	weeklyPrimary := &Auth{
		ID: "b",
		Quota: QuotaState{
			ObservedAt: now,
			Signals: map[string]string{
				"X-Codex-Primary-Reset-At":         soonestResetUnix(now.Add(2 * 24 * time.Hour)),
				"X-Codex-Primary-Window-Minutes":   "10080",
				"X-Codex-Secondary-Window-Minutes": "0",
			},
		},
	}
	noMinutes := &Auth{
		ID: "c",
		Quota: QuotaState{
			ObservedAt: now,
			Signals: map[string]string{
				"X-Codex-Primary-Reset-At": soonestResetUnix(now.Add(4 * 24 * time.Hour)),
			},
		},
	}

	if got := pickSoonestReset(t, "codex", "", shortPrimary, weeklyPrimary, noMinutes); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
	// Without b, the minutes-less primary (4d) beats a's weekly secondary (6d).
	if got := pickSoonestReset(t, "codex", "", shortPrimary, noMinutes); got.ID != "c" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "c")
	}
}

func TestSoonestResetSelectorPick_UsesNewestObservedSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// a: the top-level snapshot is old and claims a soon reset; the newer
	// model snapshot says the window resets late.
	a := claudeQuotaAuth("a", now.Add(24*time.Hour), now.Add(-time.Hour))
	a.ModelStates = map[string]*ModelState{
		"m": {
			Status: StatusActive,
			Quota: QuotaState{
				ObservedAt: now,
				Signals: map[string]string{
					"Anthropic-Ratelimit-Unified-7d-Reset": soonestResetUnix(now.Add(6 * 24 * time.Hour)),
				},
			},
		},
	}
	b := claudeQuotaAuth("b", now.Add(3*24*time.Hour), now)

	if got := pickSoonestReset(t, "claude", "", a, b); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_SkipsCooldown(t *testing.T) {
	t.Parallel()

	now := time.Now()
	model := "test-model"
	cooling := claudeQuotaAuth("a", now.Add(time.Hour), now)
	cooling.ModelStates = map[string]*ModelState{
		model: {
			Status:         StatusActive,
			Unavailable:    true,
			NextRetryAfter: now.Add(30 * time.Minute),
			Quota: QuotaState{
				Exceeded:      true,
				NextRecoverAt: now.Add(30 * time.Minute),
			},
		},
	}
	ready := claudeQuotaAuth("b", now.Add(6*24*time.Hour), now)

	if got := pickSoonestReset(t, "claude", model, cooling, ready); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_HigherPriorityWins(t *testing.T) {
	t.Parallel()

	now := time.Now()
	lowSoon := claudeQuotaAuth("a", now.Add(time.Hour), now)
	lowSoon.Attributes = map[string]string{"priority": "0"}
	highLate := claudeQuotaAuth("b", now.Add(6*24*time.Hour), now)
	highLate.Attributes = map[string]string{"priority": "10"}

	if got := pickSoonestReset(t, "claude", "", lowSoon, highLate); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}
