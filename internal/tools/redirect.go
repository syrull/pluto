package tools

import (
	"context"
	"encoding/json"
	"strings"
)

// redirectBash inspects a raw shell command and, when it is a trivially simple
// file-read or content-search invocation, executes it through the Read or Find
// tool instead of a subshell. Those tools bound their output so results can't
// overflow the model's context window, whereas an unbounded `cat`/`grep` can.
//
// Detection is deliberately conservative: any shell metacharacter (pipe,
// redirect, sequence, subshell, expansion, quoting) disqualifies the command,
// which then falls through to normal `sh -c` execution. It is always safe to
// return ("", "", false) — the caller simply runs the original command.
//
// The returned (out, err) is the tool's result when handled is true.
func redirectBash(ctx context.Context, command string) (out string, err error, handled bool) {
	toks, ok := simpleTokens(command)
	if !ok || len(toks) == 0 {
		return "", nil, false
	}

	switch toks[0] {
	case "cat":
		return redirectCat(ctx, toks[1:])
	case "grep", "rg":
		return redirectGrep(ctx, toks[0], toks[1:])
	}
	return "", nil, false
}

// simpleTokens splits command on ASCII whitespace and returns the tokens only
// when the command contains no shell-significant characters. The presence of
// any of these means the command's behavior depends on the shell (pipelines,
// redirection, expansion, quoting, sequencing), so we must not reinterpret it.
func simpleTokens(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}
	if strings.ContainsAny(command, "|&;<>()$`\"'*?[]{}\\!#~\n\t") {
		return nil, false
	}
	return strings.Fields(command), true
}

// redirectCat handles `cat FILE`: exactly one non-flag operand, no flags.
func redirectCat(ctx context.Context, args []string) (string, error, bool) {
	file, ok := singleFileOperand(args)
	if !ok {
		return "", nil, false
	}
	out, err := runRead(ctx, readArgs{Path: file})
	return out, err, true
}

// redirectGrep handles `grep [flags] PATTERN PATH` and `rg [flags] PATTERN
// [PATH]` mapping to Find. Only a small, safe flag subset is accepted; anything
// else falls through so we never change matching semantics the model relied on.
//
// Bare `grep PATTERN` reads stdin, which Find cannot emulate, so grep requires
// an explicit path. Bare `rg PATTERN` defaults to a recursive search of the
// working directory, which matches Find's default, so its path is optional.
func redirectGrep(ctx context.Context, name string, args []string) (string, error, bool) {
	var operands []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			// Common recursive-search flags are the default in Find; accept
			// and ignore them. Any other flag changes semantics — bail.
			switch a {
			case "-r", "-R", "-n", "-H", "-rn", "-rH", "-nH", "-rnH":
				continue
			default:
				return "", nil, false
			}
		}
		operands = append(operands, a)
	}
	if len(operands) > 2 {
		return "", nil, false
	}
	// grep with no file reads stdin — Find can't emulate that.
	if len(operands) < 2 && name == "grep" {
		return "", nil, false
	}
	if len(operands) == 0 {
		return "", nil, false
	}
	fa := findArgs{Pattern: operands[0]}
	if len(operands) == 2 {
		fa.Path = operands[1]
	}
	out, err := runFind(ctx, fa)
	return out, err, true
}

// singleFileOperand returns the sole operand when args holds exactly one
// non-flag token and no flags. A leading '-' disqualifies (flag or stdin).
func singleFileOperand(args []string) (string, bool) {
	if len(args) != 1 {
		return "", false
	}
	if strings.HasPrefix(args[0], "-") {
		return "", false
	}
	return args[0], true
}

// runRead invokes the Read tool with the given args.
func runRead(ctx context.Context, a readArgs) (string, error) {
	raw, _ := json.Marshal(a)
	return Read{}.Execute(ctx, raw)
}

// runFind invokes the Find tool with the given args.
func runFind(ctx context.Context, a findArgs) (string, error) {
	raw, _ := json.Marshal(a)
	return Find{}.Execute(ctx, raw)
}
