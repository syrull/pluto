// Package mode selects the operating mode of the harness: the default
// code-development flow, or the CTF offensive-engagement workflow. A mode is a
// first-class, toggleable concept — activatable by flag, env, or a live slash
// command — mirroring the PLUTO_GOAL env toggle it sits alongside.
package mode

import (
	"os"
	"strings"
)

// Mode is the harness operating mode.
type Mode string

const (
	// Default is the standard code-development harness.
	Default Mode = "default"
	// CTF is the offensive-engagement workflow harness: CTF persona, red theme,
	// parallel fan-out by default, and an engagement blackboard.
	CTF Mode = "ctf"
)

// IsCTF reports whether m is the CTF mode.
func (m Mode) IsCTF() bool { return m == CTF }

// String renders the mode, treating the empty value as Default.
func (m Mode) String() string {
	if m == "" {
		return string(Default)
	}
	return string(m)
}

// FromEnv derives the initial mode from the environment, mirroring goalEnabled:
// PLUTO_MODE=ctf, or PLUTO_CTF set to a truthy value (on|1|true|yes), selects
// CTF. Anything else — including unset — is the default mode.
func FromEnv() Mode {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PLUTO_MODE")), string(CTF)) {
		return CTF
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLUTO_CTF"))) {
	case "on", "1", "true", "yes":
		return CTF
	}
	return Default
}

// FromArgs reports whether argv (os.Args) requests CTF mode via a --ctf/-ctf
// flag anywhere on the line or a leading `ctf` subcommand.
func FromArgs(args []string) bool {
	for i, a := range args {
		if i == 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "--ctf", "-ctf", "ctf":
			return true
		}
	}
	return false
}

// Resolve combines the argv and env signals into the initial mode: an explicit
// --ctf/ctf on the command line or a CTF env toggle enters CTF, else Default.
func Resolve(args []string) Mode {
	if FromArgs(args) || FromEnv() == CTF {
		return CTF
	}
	return Default
}
