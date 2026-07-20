package skills

import (
	"embed"
	"strings"
	"sync/atomic"

	"github.com/syrull/pluto/internal/debug"
)

// ctfDir is the embed root holding the curated CTF skill playbooks that ride in
// the binary and surface only while CTF mode is active.
const ctfDir = "ctf"

//go:embed ctf
var ctfFS embed.FS

// ctfEnabled gates whether the embedded CTF skills are merged into List/Load.
// It is process-wide state toggled by the /ctf mode so both the always-on skills
// index and the skill tool see the same set. Default (false) keeps the standard
// mode's skill set unchanged.
var ctfEnabled atomic.Bool

// SetCTFMode enables or disables the embedded CTF skill set.
func SetCTFMode(on bool) {
	if ctfEnabled.Swap(on) != on {
		debug.Info("ctf", "skills set toggled", "on", on, "count", len(ctfSkills()))
	}
}

// CTFMode reports whether the embedded CTF skills are active.
func CTFMode() bool { return ctfEnabled.Load() }

// ctfSkills returns the embedded CTF skill index entries, sorted by name.
func ctfSkills() []Skill {
	entries, err := ctfFS.ReadDir(ctfDir)
	if err != nil {
		debug.Warn("ctf", "embedded skills unreadable", "err", err)
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, ok := ctfIndex(e.Name()); ok {
			out = append(out, s)
		}
	}
	return out
}

// ctfIndex reads an embedded skill's frontmatter into an index entry.
func ctfIndex(name string) (Skill, bool) {
	data, err := ctfFS.ReadFile(ctfDir + "/" + name + "/" + FileName)
	if err != nil {
		return Skill{}, false
	}
	meta, _ := parse(string(data))
	desc := oneLine(meta["description"])
	if desc == "" {
		return Skill{}, false
	}
	return Skill{Name: name, Summary: truncate(desc, maxSummaryLen)}, true
}

// ctfLoad returns the Markdown body of an embedded CTF skill with its
// frontmatter stripped, or ok=false when there is no such embedded skill.
func ctfLoad(name string) (string, bool) {
	if !safeName(name) {
		return "", false
	}
	data, err := ctfFS.ReadFile(ctfDir + "/" + name + "/" + FileName)
	if err != nil {
		return "", false
	}
	_, body := parse(string(data))
	body = strings.TrimSpace(body)
	if body == "" {
		return "", false
	}
	return body, true
}
