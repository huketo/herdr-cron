package schedule

import (
	"testing"
	"time"
)

func TestParseExprResolvesTheFormByShape(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct{ expr, form, at string }{
		{"@daily", FormCron, ""},
		{"17 3 * * 1-5", FormCron, ""},
		{"30m", FormEvery, ""},
		{"2026-12-24T18:00:00Z", FormAt, "2026-12-24T18:00:00Z"},
		// A relative one-shot resolves against now, and only the absolute instant
		// survives: "+2h" stored in jobs.yaml would re-anchor on every reload.
		{"+2h", FormAt, "2026-09-03T14:00:00Z"},
		{"+90s", FormAt, "2026-09-03T12:01:30Z"},
	} {
		spec, form, err := ParseExpr(tc.expr, now, time.UTC)
		if err != nil {
			t.Errorf("%q: %v", tc.expr, err)
			continue
		}
		if form != tc.form {
			t.Errorf("%q resolved to form %q, want %q", tc.expr, form, tc.form)
		}
		if spec.At != tc.at {
			t.Errorf("%q resolved to instant %q, want %q", tc.expr, spec.At, tc.at)
		}
	}
}

// The "+" prefix is checked before the duration form on purpose: time.ParseDuration accepts
// "+2h", so without it "run this once in two hours" would silently become "run this every two
// hours" (docs/adr/0006-one-shot-jobs-are-at-jobs-with-a-lifecycle.md).
func TestParseExprKeepsBareDurationsRecurring(t *testing.T) {
	spec, form, err := ParseExpr("2h", time.Now(), time.UTC)
	if err != nil {
		t.Fatalf("ParseExpr: %v", err)
	}
	if form != FormEvery || spec.Every != 2*time.Hour {
		t.Fatalf("2h resolved to %q/%s, want every/2h", form, spec.Every)
	}
}

func TestParseExprRejectsUnusableExpressions(t *testing.T) {
	for _, expr := range []string{"nonsense", "+", "+nonsense", "+0s", "+-1h"} {
		if _, _, err := ParseExpr(expr, time.Now(), time.UTC); err == nil {
			t.Errorf("%q was accepted; it has no schedule to name", expr)
		}
	}
}
