// Package judge evaluates a proposed shell command with a small LLM, returning a
// structured allow/block verdict. It is advisory; the caller (see the policy
// gate) decides how to enforce a verdict and what to do on judge failure.
package judge

import "context"

// Decision is the judge's verdict on a command.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionBlock Decision = "block"
)

// Request is everything the judge sees about a proposed command.
type Request struct {
	Command string   // the shell command to run
	Intent  string   // model-supplied "what this accomplishes"
	Why     string   // model-supplied rationale
	Cwd     string   // the agent's working directory (its worktree), for context
	Roots   []string // directories the agent may legitimately operate within
}

// Verdict is the parsed judge output.
type Verdict struct {
	Decision Decision
	Risk     string // none|low|medium|high|critical
	Reason   string // one line, shown to user and fed to the model on block
}

// Judge assesses a single proposed command.
type Judge interface {
	Assess(ctx context.Context, req Request) (Verdict, error)
}

// Fake is a canned Judge for tests and offline runs.
type Fake struct {
	Verdict Verdict
	Err     error
}

// Assess returns the canned verdict and error.
func (f Fake) Assess(context.Context, Request) (Verdict, error) { return f.Verdict, f.Err }
