package main

import (
	"fmt"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/ctxinspect"
	"github.com/asheshgoplani/agent-deck/internal/ctxinspect/ctxtext"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// contextLiveJSON is how full the session's context is NOW: the prompt its
// newest turn carried, against its window.
//
// The rest of `session context` measures the fixed startup overhead, which is
// the part a user can clean up. It cannot answer "is this session close to
// needing a compaction?", and a script that has to answer that — compact before
// a long build, say — was left parsing transcripts itself and guessing the
// window. The TUI's context bar already answers it, so this publishes that same
// reading ([session.SessionAnalytics.ContextUsage]) rather than a second
// computation that could drift from the bar.
//
// Like every figure in this document, an unknown is null, never 0: Tokens is
// null before the first model turn, and WindowPercent is null whenever the
// window is unknown or observed usage has disproved it.
type contextLiveJSON struct {
	// Supported is false when this harness has no live reading agent-deck can
	// take; Reason says why.
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// Tokens is the newest turn's prompt: input + cache creation + cache read.
	Tokens *int `json:"tokens"`
	// PeakTokens is the largest prompt any turn carried. It survives /clear and
	// compaction, which is why it, and not Tokens, can disprove a window.
	PeakTokens *int `json:"peak_tokens,omitempty"`
	// Window is the denominator, with its provenance.
	Window *ctxinspect.WindowInfo `json:"window,omitempty"`
	// WindowPercent is Tokens as a share of Window, 0..100.
	WindowPercent *float64 `json:"window_percent"`
	// Inferred says the window came from the model-id table, not a measurement.
	Inferred bool `json:"inferred,omitempty"`
	// OverLimit says observed usage exceeded the window, so the window is wrong
	// and there is no percentage.
	OverLimit bool `json:"over_limit,omitempty"`
	// Source names where the reading came from.
	Source string `json:"source,omitempty"`
}

// liveContextSource is the provenance every live reading carries.
const liveContextSource = "transcript: newest assistant turn's input + cache_creation + cache_read tokens (the TUI context bar's reading)"

// readLiveContext takes the live reading for a session. It never fails the
// command: anything that stops a reading becomes Supported/Reason, because the
// startup-overhead report around it is still worth printing.
//
// The transcript is the one `session show` reports ([session.LiveTranscriptPath]),
// which resolves through the instance's own Claude config dir, so a session on
// another account is read from that account's projects/ and not this process's.
// peers are the profile's other sessions: a transcript shared by two live
// instances belongs to neither (issue #1349), so it is refused, not read.
func readLiveContext(inst *session.Instance, peers []*session.Instance) *contextLiveJSON {
	if inst == nil {
		return nil
	}
	if tool := inst.GetToolThreadSafe(); !session.IsClaudeCompatible(tool) {
		return &contextLiveJSON{
			Supported: false,
			Reason:    fmt.Sprintf("the live reading comes from Claude transcripts; %s sessions are not read yet", displayTool(tool)),
		}
	}
	if inst.ClaudeSessionIDCollidesWith(peers) {
		return &contextLiveJSON{Supported: true, Reason: "this session's Claude session id is shared with another live session, so its transcript is not read"}
	}
	path := session.LiveTranscriptPath(inst, peers)
	if strings.TrimSpace(path) == "" {
		return &contextLiveJSON{Supported: true, Reason: "no transcript yet, so nothing has been loaded into the context"}
	}
	analytics, err := session.ParseSessionJSONL(path)
	if err != nil {
		return &contextLiveJSON{Supported: true, Reason: "reading the transcript: " + err.Error()}
	}
	return liveContextFromAnalytics(analytics)
}

// liveContextFromAnalytics turns parsed analytics into the live reading. It is
// separate from [readLiveContext] so the encoding rules can be tested without
// a session or a transcript on disk.
func liveContextFromAnalytics(a *session.SessionAnalytics) *contextLiveJSON {
	doc := &contextLiveJSON{Supported: true, Source: liveContextSource}
	if a == nil || a.PeakContextTokens == 0 {
		doc.Reason = "no model turn has carried a prompt yet"
		return doc
	}
	usage := a.ContextUsage()
	current, peak := a.CurrentContextTokens, a.PeakContextTokens
	doc.Tokens = &current
	doc.PeakTokens = &peak
	window := usage.Window
	doc.Window = &window
	doc.Inferred = usage.Inferred
	doc.OverLimit = usage.OverLimit
	if usage.Known {
		pct := usage.Percent
		doc.WindowPercent = &pct
	}
	return doc
}

// liveContextQuiet is the clause the one-line mode appends, or "" when there is
// no figure to give. One line has room for the number, not the reasons.
func liveContextQuiet(l *contextLiveJSON) string {
	if l == nil || l.Tokens == nil {
		return ""
	}
	if l.WindowPercent == nil {
		return fmt.Sprintf("; live: %s in use now (no window size, so no percentage)", formatTokenAmount(*l.Tokens))
	}
	clause := fmt.Sprintf("; live: %s / %s (%s) in use now",
		formatTokenAmount(*l.Tokens), formatTokenAmount(l.Window.Tokens), ctxtext.PercentText(*l.Window, *l.WindowPercent))
	if l.Inferred {
		clause += " (window assumed)"
	}
	return clause
}
