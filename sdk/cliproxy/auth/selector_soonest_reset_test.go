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

func TestSoonestResetSelectorPick_CaseInsensitiveAndUnifiedResetIsUnknown(t *testing.T) {
	t.Parallel()

	now := time.Now()
	later := claudeQuotaAuth("a", now.Add(5*24*time.Hour), now)
	lowerCase := &Auth{
		ID: "b",
		Quota: QuotaState{
			ObservedAt: now,
			Signals: map[string]string{
				"anthropic-ratelimit-unified-7d-reset": soonestResetUnix(now.Add(24 * time.Hour)),
			},
		},
	}

	if got := pickSoonestReset(t, "claude", "", later, lowerCase); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}

	// The generic unified reset may describe the 5-hour window, so a credential
	// carrying only that header has an unknown weekly reset and goes after a
	// known 7d reset even though its value is earlier.
	unifiedOnly := &Auth{
		ID: "0",
		Quota: QuotaState{
			ObservedAt: now,
			Signals: map[string]string{
				"Anthropic-Ratelimit-Unified-Reset": soonestResetUnix(now.Add(time.Hour)),
			},
		},
	}

	if got := pickSoonestReset(t, "claude", "", later, unifiedOnly); got.ID != "a" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "a")
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
	// Without b, a's weekly secondary (6d) beats the minutes-less primary,
	// whose reset is unknown because its window length is unknown.
	if got := pickSoonestReset(t, "codex", "", shortPrimary, noMinutes); got.ID != "a" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "a")
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

func codexQuotaAuth(id string, signals map[string]string, observedAt time.Time) *Auth {
	return &Auth{ID: id, Quota: QuotaState{ObservedAt: observedAt, Signals: signals}}
}

func TestSoonestResetSelectorPick_CodexWeeklySlotResetUsed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// The weekly window sits in the primary slot; the short secondary window
	// resets much sooner but must not decide.
	weekly := codexQuotaAuth("0", map[string]string{
		"X-Codex-Primary-Reset-At":         soonestResetUnix(now.Add(4 * 24 * time.Hour)),
		"X-Codex-Primary-Window-Minutes":   "10080",
		"X-Codex-Secondary-Reset-At":       soonestResetUnix(now.Add(time.Hour)),
		"X-Codex-Secondary-Window-Minutes": "300",
	}, now)
	sooner := claudeQuotaAuth("a", now.Add(2*24*time.Hour), now)
	later := claudeQuotaAuth("b", now.Add(6*24*time.Hour), now)

	if got := pickSoonestReset(t, "codex", "", weekly, sooner); got.ID != "a" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "a")
	}
	if got := pickSoonestReset(t, "codex", "", weekly, later); got.ID != "0" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "0")
	}
}

func TestSoonestResetSelectorPick_CodexWeeklySlotMissingResetIsUnknown(t *testing.T) {
	t.Parallel()

	now := time.Now()
	known := claudeQuotaAuth("z", now.Add(6*24*time.Hour), now)
	// Weekly window in the secondary slot without a reset; the short primary
	// window's earlier reset must not stand in for it.
	weeklySecondary := codexQuotaAuth("a", map[string]string{
		"X-Codex-Primary-Reset-At":         soonestResetUnix(now.Add(time.Hour)),
		"X-Codex-Primary-Window-Minutes":   "300",
		"X-Codex-Secondary-Window-Minutes": "10080",
	}, now)
	// Weekly window in the primary slot without a reset; the short secondary
	// window's earlier reset must not stand in for it.
	weeklyPrimary := codexQuotaAuth("b", map[string]string{
		"X-Codex-Primary-Window-Minutes":   "10080",
		"X-Codex-Secondary-Reset-At":       soonestResetUnix(now.Add(time.Hour)),
		"X-Codex-Secondary-Window-Minutes": "300",
	}, now)

	if got := pickSoonestReset(t, "codex", "", weeklySecondary, known); got.ID != "z" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "z")
	}
	if got := pickSoonestReset(t, "codex", "", weeklyPrimary, known); got.ID != "z" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "z")
	}
}

func TestSoonestResetSelectorPick_CodexNoWindowMinutesIsUnknown(t *testing.T) {
	t.Parallel()

	now := time.Now()
	noMinutes := codexQuotaAuth("0", map[string]string{
		"X-Codex-Primary-Reset-At":   soonestResetUnix(now.Add(2 * 24 * time.Hour)),
		"X-Codex-Secondary-Reset-At": soonestResetUnix(now.Add(time.Hour)),
	}, now)
	secondaryOnly := codexQuotaAuth("1", map[string]string{
		"X-Codex-Secondary-Reset-At": soonestResetUnix(now.Add(time.Hour)),
	}, now)
	later := claudeQuotaAuth("b", now.Add(3*24*time.Hour), now)

	// Without window minutes neither slot can be confirmed as the weekly
	// window, so both resets are unknown and a later known reset wins.
	if got := pickSoonestReset(t, "codex", "", noMinutes, later); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
	if got := pickSoonestReset(t, "codex", "", secondaryOnly, later); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

type soonestResetTestCustomSelector struct{}

func (soonestResetTestCustomSelector) Pick(context.Context, string, string, cliproxyexecutor.Options, []*Auth) (*Auth, error) {
	return nil, nil
}

func TestSelectorContextForAvailableAuths_SoonestResetIsPrevalidated(t *testing.T) {
	t.Parallel()

	ctx := selectorContextForAvailableAuths(context.Background(), &SoonestResetSelector{}, "route-model")
	if validated, _ := ctx.Value(prevalidatedAuthCandidatesKey{}).(bool); !validated {
		t.Fatalf("SoonestResetSelector context prevalidated = false, want true")
	}

	ctx = selectorContextForAvailableAuths(context.Background(), soonestResetTestCustomSelector{}, "route-model")
	if validated, _ := ctx.Value(prevalidatedAuthCandidatesKey{}).(bool); validated {
		t.Fatalf("custom selector context prevalidated = true, want false")
	}
}

func TestSoonestResetSelectorPick_CodexFiveHourOnlyIsUnknown(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// The snapshot carries only the 5-hour window; its reset must not rank as
	// the weekly reset.
	fiveHourOnly := codexQuotaAuth("a", map[string]string{
		"X-Codex-Primary-Reset-At":       soonestResetUnix(now.Add(time.Hour)),
		"X-Codex-Primary-Window-Minutes": "300",
	}, now)
	weekly := codexQuotaAuth("b", map[string]string{
		"X-Codex-Primary-Reset-At":       soonestResetUnix(now.Add(5 * 24 * time.Hour)),
		"X-Codex-Primary-Window-Minutes": "10080",
	}, now)

	if got := pickSoonestReset(t, "codex", "", fiveHourOnly, weekly); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_CodexResetAfterSecondsUsesObservedAt(t *testing.T) {
	t.Parallel()

	now := time.Now()
	observedAt := now.Add(-time.Hour)
	// Weekly secondary slot with only Reset-After-Seconds: it resets at
	// observedAt + 2d, about 47h from now.
	resetAfterOnly := codexQuotaAuth("z", map[string]string{
		"X-Codex-Primary-Reset-At":              soonestResetUnix(now.Add(time.Hour)),
		"X-Codex-Primary-Window-Minutes":        "300",
		"X-Codex-Secondary-Reset-After-Seconds": strconv.FormatInt(int64((2 * 24 * time.Hour).Seconds()), 10),
		"X-Codex-Secondary-Window-Minutes":      "10080",
	}, observedAt)
	later := codexQuotaAuth("a", map[string]string{
		"X-Codex-Primary-Reset-At":       soonestResetUnix(now.Add(3 * 24 * time.Hour)),
		"X-Codex-Primary-Window-Minutes": "10080",
	}, now)
	sooner := codexQuotaAuth("b", map[string]string{
		"X-Codex-Primary-Reset-At":       soonestResetUnix(now.Add(46 * time.Hour)),
		"X-Codex-Primary-Window-Minutes": "10080",
	}, now)

	if got := pickSoonestReset(t, "codex", "", resetAfterOnly, later); got.ID != "z" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "z")
	}
	if got := pickSoonestReset(t, "codex", "", resetAfterOnly, sooner); got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_CodexResetAfterSecondsWithoutObservedAtIsUnknown(t *testing.T) {
	t.Parallel()

	now := time.Now()
	noObservedAt := codexQuotaAuth("a", map[string]string{
		"X-Codex-Primary-Reset-After-Seconds": strconv.FormatInt(int64((24 * time.Hour).Seconds()), 10),
		"X-Codex-Primary-Window-Minutes":      "10080",
	}, time.Time{})
	known := codexQuotaAuth("z", map[string]string{
		"X-Codex-Primary-Reset-At":       soonestResetUnix(now.Add(6 * 24 * time.Hour)),
		"X-Codex-Primary-Window-Minutes": "10080",
	}, now)

	if got := pickSoonestReset(t, "codex", "", noObservedAt, known); got.ID != "z" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "z")
	}
}
