package main

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/ctxinspect/ctxtext"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// The live reading publishes the TUI context bar's number, so these tests pin
// it to ContextUsage rather than re-deriving a percentage.

func TestLiveContextCarriesTheContextBarReading(t *testing.T) {
	t.Setenv(ctxtext.WindowEnvVar, "")
	a := &session.SessionAnalytics{Model: "claude-opus-5", CurrentContextTokens: 250000, PeakContextTokens: 300000}
	got := liveContextFromAnalytics(a)
	want := a.ContextUsage()

	if !got.Supported || got.Reason != "" {
		t.Fatalf("a parsed transcript is a supported reading with no reason, got %+v", got)
	}
	if got.Tokens == nil || *got.Tokens != 250000 {
		t.Fatalf("Tokens = %v, want the newest turn's prompt (250000)", got.Tokens)
	}
	if got.PeakTokens == nil || *got.PeakTokens != 300000 {
		t.Fatalf("PeakTokens = %v, want 300000", got.PeakTokens)
	}
	if got.WindowPercent == nil || *got.WindowPercent != want.Percent {
		t.Fatalf("WindowPercent = %v, want the context bar's %v", got.WindowPercent, want.Percent)
	}
	if got.Window == nil || got.Window.Tokens != want.Window.Tokens || !got.Inferred {
		t.Fatalf("window must be the bar's, marked inferred: %+v", got)
	}
}

// Before any turn there is no reading: null, never 0.
func TestLiveContextBeforeTheFirstTurnIsNullNotZero(t *testing.T) {
	for name, a := range map[string]*session.SessionAnalytics{"nil analytics": nil, "no turn yet": {}} {
		got := liveContextFromAnalytics(a)
		if got.Tokens != nil || got.WindowPercent != nil {
			t.Errorf("%s: an absent reading must encode as null, got tokens=%v pct=%v", name, got.Tokens, got.WindowPercent)
		}
		if got.Reason == "" {
			t.Errorf("%s: an absent reading must say why", name)
		}
		b := marshalContext(t, got)
		if !strings.Contains(b, `"tokens": null`) || !strings.Contains(b, `"window_percent": null`) {
			t.Errorf("%s: want explicit nulls on the wire, got\n%s", name, b)
		}
	}
}

// A window observed usage has disproved gives no percentage; the tokens still
// stand, because they are measured.
func TestLiveContextOverLimitHasNoPercentage(t *testing.T) {
	t.Setenv(ctxtext.WindowEnvVar, "")
	a := &session.SessionAnalytics{Model: "claude-opus-4-20250514", CurrentContextTokens: 40000, PeakContextTokens: 494561}
	got := liveContextFromAnalytics(a)
	if !got.OverLimit || got.WindowPercent != nil {
		t.Fatalf("a disproved window must be OverLimit with a null percentage, got %+v", got)
	}
	if got.Tokens == nil || *got.Tokens != 40000 {
		t.Fatalf("Tokens = %v, want the measured 40000 even over the limit", got.Tokens)
	}
	if line := liveContextQuiet(got); !strings.Contains(line, "no window size") {
		t.Errorf("the one-line mode must not print a percentage for a disproved window, got %q", line)
	}
}

func TestLiveContextQuietClause(t *testing.T) {
	t.Setenv(ctxtext.WindowEnvVar, "")
	a := &session.SessionAnalytics{Model: "claude-opus-5", CurrentContextTokens: 298057, PeakContextTokens: 298057}
	line := liveContextQuiet(liveContextFromAnalytics(a))
	if !strings.HasPrefix(line, "; live: ") || !strings.Contains(line, "in use now") || !strings.Contains(line, "(window assumed)") {
		t.Fatalf("quiet clause = %q", line)
	}
	if liveContextQuiet(nil) != "" || liveContextQuiet(liveContextFromAnalytics(nil)) != "" {
		t.Fatal("no reading must add nothing to the one-line mode")
	}
}

// The key is additive: a view with no live reading must produce the same
// document as before this field existed, which is what keeps the goldens and
// the schema version unchanged.
func TestBuildContextJSONOmitsLiveWhenNoReadingWasTaken(t *testing.T) {
	v := contextTestView(t)
	if got := decodeContext(t, buildContextJSON(v)); got["live"] != nil {
		t.Fatalf("live must be absent when no reading was taken, got %v", got["live"])
	}
	t.Setenv(ctxtext.WindowEnvVar, "")
	v.Live = liveContextFromAnalytics(&session.SessionAnalytics{Model: "claude-opus-5", CurrentContextTokens: 1000, PeakContextTokens: 1000})
	live, ok := decodeContext(t, buildContextJSON(v))["live"].(map[string]interface{})
	if !ok || live["tokens"] != float64(1000) {
		t.Fatalf("live block missing or wrong: %v", live)
	}
	if !strings.Contains(renderContextQuiet(v), "; live: ") {
		t.Fatalf("the one-line mode must carry the live clause when there is a reading: %q", renderContextQuiet(v))
	}
}

// A non-Claude session gets an honest "not supported", not a fake reading.
func TestReadLiveContextNonClaudeIsUnsupported(t *testing.T) {
	inst := &session.Instance{ID: "x", Tool: "codex"}
	got := readLiveContext(inst, nil)
	if got == nil || got.Supported || got.Reason == "" || got.Tokens != nil {
		t.Fatalf("a non-Claude session must be unsupported with a reason and no figure, got %+v", got)
	}
	if readLiveContext(nil, nil) != nil {
		t.Fatal("no session, no reading")
	}
}
