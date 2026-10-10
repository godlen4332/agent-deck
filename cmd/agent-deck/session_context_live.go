package main

import (
	"context"
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
	// PossiblyStale says some transcript lines could not be read (ParseGaps of
	// them), and a skipped line can be the newest usage record — so Tokens may
	// be an older turn's, higher or lower than the real current use. The TUI
	// panel labels the same case.
	PossiblyStale bool `json:"possibly_stale,omitempty"`
	ParseGaps     int  `json:"parse_gaps,omitempty"`
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
//
// The scan is bounded by ctx (the command's --timeout): a huge transcript on a
// slow filesystem yields a reason, not a hung command.
func readLiveContext(ctx context.Context, inst *session.Instance, peers []*session.Instance) *contextLiveJSON {
	if inst == nil {
		return nil
	}
	if tool := inst.GetToolThreadSafe(); !session.IsClaudeCompatible(tool) {
		return &contextLiveJSON{
			Supported: false,
			Reason:    fmt.Sprintf("the live reading comes from Claude transcripts; %s sessions are not read yet", displayTool(tool)),
		}
	}
	const timedOut = "reading the transcript did not finish within --timeout"
	if ctx.Err() != nil {
		return &contextLiveJSON{Supported: true, Reason: timedOut}
	}
	if inst.ClaudeSessionIDCollidesWith(peers) {
		return &contextLiveJSON{Supported: true, Reason: "this session's Claude session id is shared with another live session, so its transcript is not read"}
	}
	path := session.LiveTranscriptPath(inst, peers)
	if strings.TrimSpace(path) == "" {
		return &contextLiveJSON{Supported: true, Reason: "no transcript yet, so nothing has been loaded into the context"}
	}
	type parsed struct {
		a   *session.SessionAnalytics
		err error
	}
	done := make(chan parsed, 1) // buffered: a timed-out parse must not block forever on send
	go func() {
		a, err := session.ParseSessionJSONL(path)
		done <- parsed{a, err}
	}()
	select {
	case <-ctx.Done():
		return &contextLiveJSON{Supported: true, Reason: timedOut}
	case r := <-done:
		if r.err != nil {
			return &contextLiveJSON{Supported: true, Reason: "reading the transcript: " + r.err.Error()}
		}
		return liveContextFromAnalytics(r.a)
	}
}

// liveContextFromAnalytics turns parsed analytics into the live reading. It is
// separate from [readLiveContext] so the encoding rules can be tested without
// a session or a transcript on disk.
func liveContextFromAnalytics(a *session.SessionAnalytics) *contextLiveJSON {
	doc := &contextLiveJSON{Supported: true, Source: liveContextSource}
	if a.HasParseGaps() {
		doc.PossiblyStale = true
		doc.ParseGaps = a.ParseGaps
	}
	if a == nil || a.PeakContextTokens == 0 {
		doc.Reason = "no model turn has carried a prompt yet"
		if doc.PossiblyStale {
			// Unreadable, not unused: every usage record may be among the gaps.
			doc.Reason = fmt.Sprintf("no usage record could be read, and %d transcript line%s could not be parsed, so the context use is unknown", a.ParseGaps, plural(a.ParseGaps))
		}
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
	if doc.PossiblyStale {
		// Not a bound in either direction: a skipped record before a compaction
		// would make this low, one after it would make this high.
		doc.Reason = fmt.Sprintf("%d transcript line%s could not be read; the newest usage record may be among them, so the current use may be higher or lower than this reading", a.ParseGaps, plural(a.ParseGaps))
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
		return fmt.Sprintf("; live: %s in use now (no window size, so no percentage)", formatTokenAmount(*l.Tokens)) + liveStaleQuiet(l)
	}
	clause := fmt.Sprintf("; live: %s / %s (%s) in use now",
		formatTokenAmount(*l.Tokens), formatTokenAmount(l.Window.Tokens), ctxtext.PercentText(*l.Window, *l.WindowPercent))
	if l.Inferred {
		clause += " (window assumed)"
	}
	return clause + liveStaleQuiet(l)
}

// liveStaleQuiet is the one-line qualifier for a reading with parse gaps.
func liveStaleQuiet(l *contextLiveJSON) string {
	if !l.PossiblyStale {
		return ""
	}
	return " (possibly stale: unreadable transcript lines)"
}
