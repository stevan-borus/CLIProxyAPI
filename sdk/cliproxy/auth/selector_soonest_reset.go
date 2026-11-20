package auth

import (
	"context"
	"strconv"
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// SoonestResetSelector picks the eligible credential whose long (weekly) quota
// window resets soonest, so quota about to roll over is spent before quota that
// resets later. Credentials without a known future reset time go last; ties and
// unknowns fall back to ID order, matching FillFirstSelector.
type SoonestResetSelector struct{}

// Pick selects the available auth whose long quota window resets soonest.
func (s *SoonestResetSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	_ = opts
	now := time.Now()
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)

	var best *Auth
	var bestReset time.Time
	bestKnown := false
	for _, candidate := range available {
		reset, known := longWindowResetAt(candidate, now)
		if best == nil || resetsBefore(reset, known, candidate.ID, bestReset, bestKnown, best.ID) {
			best, bestReset, bestKnown = candidate, reset, known
		}
	}
	return best, nil
}

// resetsBefore orders known reset times ascending ahead of unknown ones, then by ID.
func resetsBefore(aReset time.Time, aKnown bool, aID string, bReset time.Time, bKnown bool, bID string) bool {
	if aKnown != bKnown {
		return aKnown
	}
	if aKnown && !aReset.Equal(bReset) {
		return aReset.Before(bReset)
	}
	return aID < bID
}

// codexLongWindowMinMinutes is the shortest Codex window that counts as the
// long window; 5-hour windows must never rank as the weekly window.
const codexLongWindowMinMinutes = 24 * 60

// longWindowResetAt returns when the auth's long quota window resets, taken from
// the newest observed quota snapshot. It reports false when no future reset time
// is known.
func longWindowResetAt(auth *Auth, now time.Time) (time.Time, bool) {
	signals, observedAt := newestQuotaSignals(auth)
	if len(signals) == 0 {
		return time.Time{}, false
	}

	// Only the 7d header is used for Claude: the generic unified reset can
	// describe the 5-hour window.
	reset, ok := parseResetUnix(quotaSignal(signals, "Anthropic-Ratelimit-Unified-7d-Reset"))
	if !ok {
		reset, ok = codexLongWindowResetAt(signals, observedAt)
	}
	if !ok || !reset.After(now) {
		return time.Time{}, false
	}
	return reset, true
}

// codexLongWindowResetAt returns the reset of the Codex slot with the larger
// window of at least codexLongWindowMinMinutes (primary on a tie). The reset is
// unknown when no slot qualifies, with no fallback to the shorter window. The
// slot's Reset-At wins; otherwise Reset-After-Seconds counts from observedAt.
func codexLongWindowResetAt(signals map[string]string, observedAt time.Time) (time.Time, bool) {
	slot := ""
	var slotMinutes int64
	for _, name := range []string{"Primary", "Secondary"} {
		minutes, err := strconv.ParseInt(quotaSignal(signals, "X-Codex-"+name+"-Window-Minutes"), 10, 64)
		if err == nil && minutes >= codexLongWindowMinMinutes && minutes > slotMinutes {
			slot, slotMinutes = name, minutes
		}
	}
	if slot == "" {
		return time.Time{}, false
	}
	if reset, ok := parseResetUnix(quotaSignal(signals, "X-Codex-"+slot+"-Reset-At")); ok {
		return reset, true
	}
	seconds, err := strconv.ParseInt(quotaSignal(signals, "X-Codex-"+slot+"-Reset-After-Seconds"), 10, 64)
	if err != nil || seconds < 0 || observedAt.IsZero() {
		return time.Time{}, false
	}
	return observedAt.Add(time.Duration(seconds) * time.Second), true
}

// newestQuotaSignals returns the quota signals with the newest ObservedAt among
// the auth-level and per-model snapshots, along with that ObservedAt. Quota
// windows are account-wide, so the most recent observation wins regardless of
// which model produced it.
func newestQuotaSignals(auth *Auth) (map[string]string, time.Time) {
	if auth == nil {
		return nil, time.Time{}
	}
	signals := auth.Quota.Signals
	observedAt := auth.Quota.ObservedAt
	if len(signals) == 0 {
		observedAt = time.Time{}
	}
	for _, state := range auth.ModelStates {
		if state == nil || len(state.Quota.Signals) == 0 {
			continue
		}
		if len(signals) == 0 || state.Quota.ObservedAt.After(observedAt) {
			signals = state.Quota.Signals
			observedAt = state.Quota.ObservedAt
		}
	}
	return signals, observedAt
}

func quotaSignal(signals map[string]string, key string) string {
	for name, value := range signals {
		if strings.EqualFold(name, key) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseResetUnix(value string) (time.Time, bool) {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}
