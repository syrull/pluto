// Package goal evaluates whether a user-set completion condition is demonstrably
// satisfied by what a coding agent has surfaced in its transcript, returning a
// structured met/not-met verdict with a short reason. It is the small, fast,
// transcript-only evaluator behind the /goal turn loop, modeled directly on
// internal/judge: it has no tools and never runs commands, reads files, or
// verifies anything itself — it judges only the visible transcript.
package goal

import "context"

// Request is everything the evaluator sees: the completion condition set by the
// user and a rendered, truncated view of the conversation so far.
type Request struct {
	// Condition is the user's completion condition.
	Condition string
	// Transcript is a rendered, already-truncated view of the agent's work so
	// far (most recent activity), the only evidence the evaluator may use.
	Transcript string
}

// Verdict is the parsed evaluator output.
type Verdict struct {
	// Met reports whether the condition is demonstrably satisfied.
	Met bool
	// Reason is one short sentence explaining the verdict, shown to the user and
	// fed back as guidance to the next turn on a "not met".
	Reason string
}

// Evaluator judges whether a condition is met given the transcript so far.
type Evaluator interface {
	Evaluate(ctx context.Context, req Request) (Verdict, error)
}

// Fake is a canned Evaluator for tests and offline runs.
type Fake struct {
	Verdict Verdict
	Err     error
}

// Evaluate returns the canned verdict and error.
func (f Fake) Evaluate(context.Context, Request) (Verdict, error) { return f.Verdict, f.Err }
