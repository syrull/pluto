// Package guard is an offline, deterministic denylist for catastrophic shell
// commands. It is a backstop, not a sandbox: it catches a small set of
// high-precision, system-destroying patterns before any subshell or judge runs.
// It cannot catch arbitrary obfuscation — that is the judge's job.
package guard

import (
	"regexp"
	"strings"

	"github.com/syrull/pluto/internal/debug"
)

// Violation describes a matched catastrophic pattern.
type Violation struct {
	// Rule is a stable identifier for the matched pattern, e.g. "rm-rf-root".
	Rule string
	// Reason is a human-readable explanation shown to the user and fed to the model.
	Reason string
}

// Check reports the first catastrophic pattern command matches. ok=false means
// no hardcoded rule fired; the command is still subject to the judge.
func Check(command string) (Violation, bool) {
	norm := normalize(command)
	if norm == "" {
		return Violation{}, false
	}
	if v, ok := checkWhole(norm); ok {
		debug.Warn("guard", "catastrophic pattern matched", "rule", v.Rule)
		return v, true
	}
	for _, seg := range segments(norm) {
		if v, ok := checkSegment(seg); ok {
			debug.Warn("guard", "catastrophic pattern matched", "rule", v.Rule)
			return v, true
		}
	}
	return Violation{}, false
}

var (
	wsRe             = regexp.MustCompile(`\s+`)
	segRe            = regexp.MustCompile(`\|\||&&|;|\||&|\n`)
	forkBombRe       = regexp.MustCompile(`\w*\(\)\{[^}]*\|[^}]*&[^}]*\}`)
	curlPipeShRe     = regexp.MustCompile(`(?i)\b(curl|wget|fetch)\b[^|]*\|\s*(sudo\s+)?(sh|bash|zsh|dash|ksh|fish)\b`)
	criticalRootRe   = regexp.MustCompile(`^/(etc|usr|bin|sbin|lib|lib64|boot|var|root|home|sys|proc|dev|opt)(/\*?)?$`)
	deviceRedirectRe = regexp.MustCompile(`>\s*/dev/(sd|hd|nvme|vd|mmcblk|disk|mem|port)`)
	ddOfDeviceRe     = regexp.MustCompile(`^of=/dev/(sd|hd|nvme|vd|mmcblk|disk)`)
	devicePathRe     = regexp.MustCompile(`^/dev/(sd|hd|nvme|vd|mmcblk|disk)`)
)

var (
	rmViolation              = Violation{Rule: "rm-rf-root", Reason: "recursive delete targeting a filesystem root or home directory"}
	forkBombViolation        = Violation{Rule: "fork-bomb", Reason: "fork bomb: self-replicating process will exhaust system resources"}
	curlPipeShViolation      = Violation{Rule: "curl-pipe-sh", Reason: "piping a remote download straight into a shell executes untrusted code"}
	mkfsViolation            = Violation{Rule: "mkfs", Reason: "formatting a filesystem destroys all data on the target"}
	ddViolation              = Violation{Rule: "dd-to-device", Reason: "writing raw data to a block device destroys its contents"}
	overwriteDeviceViolation = Violation{Rule: "overwrite-device", Reason: "redirecting output into a block device corrupts the disk"}
	diskWipeViolation        = Violation{Rule: "disk-wipe", Reason: "wiping a block device destroys all data on it"}
	permViolation            = Violation{Rule: "chmod-chown-root", Reason: "recursive permission change on a filesystem root breaks the system"}
)

func normalize(s string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

func segments(norm string) []string {
	var out []string
	for _, p := range segRe.Split(norm, -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// checkWhole applies rules that span pipes/sequences and must see the whole command.
func checkWhole(norm string) (Violation, bool) {
	condensed := strings.ReplaceAll(norm, " ", "")
	if strings.Contains(condensed, ":(){:|:&};:") || forkBombRe.MatchString(condensed) {
		return forkBombViolation, true
	}
	if curlPipeShRe.MatchString(norm) {
		return curlPipeShViolation, true
	}
	return Violation{}, false
}

// checkSegment applies rules to a single pipeline segment.
func checkSegment(seg string) (Violation, bool) {
	if deviceRedirectRe.MatchString(seg) {
		return overwriteDeviceViolation, true
	}
	toks := tokenize(seg)
	cmd, rest := commandAndArgs(toks)
	switch {
	case cmd == "rm":
		if isRmCatastrophic(rest) {
			return rmViolation, true
		}
	case cmd == "mkfs" || strings.HasPrefix(cmd, "mkfs."):
		return mkfsViolation, true
	case cmd == "chmod" || cmd == "chown":
		if isPermCatastrophic(rest) {
			return permViolation, true
		}
	case cmd == "shred" || cmd == "wipefs":
		if hasDevice(rest) {
			return diskWipeViolation, true
		}
	case cmd == "dd":
		if ddToDevice(rest) {
			return ddViolation, true
		}
	}
	return Violation{}, false
}

func tokenize(seg string) []string {
	fields := strings.Fields(seg)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, unquote(f))
	}
	return out
}

func unquote(s string) string {
	for len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = s[1 : len(s)-1]
			continue
		}
		break
	}
	return s
}

// commandAndArgs strips leading sudo/wrappers and env-var assignments, returning
// the effective command and its arguments.
func commandAndArgs(toks []string) (string, []string) {
	i := 0
	for i < len(toks) {
		t := toks[i]
		if t == "sudo" || t == "command" || t == "nice" || t == "time" || t == "-" {
			i++
			continue
		}
		if !strings.HasPrefix(t, "-") && strings.Contains(t, "=") {
			i++ // VAR=value env assignment before the command
			continue
		}
		break
	}
	if i >= len(toks) {
		return "", nil
	}
	return toks[i], toks[i+1:]
}

func isRmCatastrophic(args []string) bool {
	recursive := false
	var operands []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--"):
			if a == "--recursive" {
				recursive = true
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.ContainsAny(a[1:], "rR") {
				recursive = true
			}
		default:
			operands = append(operands, a)
		}
	}
	if !recursive {
		return false // a non-recursive rm cannot wipe a tree
	}
	for _, op := range operands {
		if isDangerousPath(op) {
			return true
		}
	}
	return false
}

func isPermCatastrophic(args []string) bool {
	recursive := false
	var operands []string
	for _, a := range args {
		switch {
		case a == "--recursive":
			recursive = true
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.ContainsAny(a[1:], "rR") {
				recursive = true
			}
		default:
			operands = append(operands, a)
		}
	}
	if !recursive {
		return false
	}
	for _, op := range operands {
		if isDangerousPath(op) {
			return true
		}
	}
	return false
}

func isDangerousPath(p string) bool {
	if p == "" {
		return false
	}
	q := p
	if len(q) > 1 {
		q = strings.TrimRight(q, "/")
	}
	switch q {
	case "/", "/*", "~", "$HOME", "${HOME}", ".", "..", "*":
		return true
	}
	if strings.Trim(p, "/") == "" {
		return true // all slashes ⇒ root
	}
	return criticalRootRe.MatchString(p)
}

func ddToDevice(args []string) bool {
	for _, a := range args {
		if ddOfDeviceRe.MatchString(a) {
			return true
		}
	}
	return false
}

func hasDevice(args []string) bool {
	for _, a := range args {
		if devicePathRe.MatchString(a) {
			return true
		}
	}
	return false
}
