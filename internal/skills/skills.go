// Package skills discovers on-demand playbooks stored as flat text files under
// a skills/ directory. Only a compact index (name + one-line summary) rides in
// the system prompt; a skill's full body is loaded lazily via the skill tool,
// keeping the always-on prompt small.
package skills

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syrull/pluto/internal/debug"
)

// DirName is the conventional directory, relative to the working directory,
// that holds skill playbooks.
const DirName = "skills"

const (
	maxSummaryLen  = 100
	maxSummaryScan = 64 * 1024
)

// exts are the file extensions treated as skills, in preference order for Load.
var exts = []string{".md", ".txt"}

// Skill is a discovered playbook's index entry: its name (the filename without
// extension) and a one-line summary derived from the file's first line.
type Skill struct {
	Name    string
	Summary string
}

// List discovers skills under dir, sorted by name. It is best-effort: a missing
// or unreadable directory yields nil, and files without a summary (empty or
// whitespace-only) are skipped since they can't be loaded either.
func List(dir string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			debug.Warn("tool", "skills dir unreadable", "dir", dir, "err", err)
		}
		return nil
	}
	// Dedupe by name: when the same basename exists with several allowed
	// extensions, keep the one Load would resolve (earliest in exts) so the index
	// never advertises an entry whose body can't be loaded by that name.
	seen := make(map[string]ranked)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rank := extRank(strings.ToLower(filepath.Ext(name)))
		if rank < 0 {
			continue
		}
		summary := readSummary(filepath.Join(dir, name))
		if summary == "" {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		cur := ranked{Skill{Name: base, Summary: summary}, rank}
		if prev, ok := seen[base]; ok {
			keep, drop := cur, prev
			if prev.rank <= cur.rank {
				keep, drop = prev, cur
			}
			debug.Debug("tool", "duplicate skill name; kept higher-preference file",
				"name", base, "kept", exts[keep.rank], "dropped", exts[drop.rank])
			seen[base] = keep
			continue
		}
		seen[base] = cur
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]Skill, 0, len(seen))
	for _, r := range seen {
		out = append(out, r.skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ranked pairs a discovered skill with its extension preference rank (its index
// in exts) so List resolves same-name collisions the way Load does.
type ranked struct {
	skill Skill
	rank  int
}

// Load returns the full text of the named skill under dir. name may be given
// with or without a known extension. It errors on an unknown, empty, or unsafe
// name.
func Load(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("skills: name is required")
	}
	if !safeName(name) {
		return "", fmt.Errorf("skills: invalid skill name %q", name)
	}
	base := Canonical(name)
	for _, ext := range exts {
		path := filepath.Join(dir, base+ext)
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				debug.Warn("tool", "skill file unreadable", "path", path, "err", err)
			}
			continue
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			return "", fmt.Errorf("skills: skill %q is empty", base)
		}
		return body, nil
	}
	return "", fmt.Errorf("skills: skill %q not found", base)
}

// Canonical strips a trailing known skill extension from name, mirroring how Load
// resolves a file, so callers can display the same name shown in the index.
func Canonical(name string) string {
	name = strings.TrimSpace(name)
	if allowedExt(strings.ToLower(filepath.Ext(name))) {
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
	return name
}

// Render formats skills as compact "- name: summary" lines for the always-on
// index, or "" when the list is empty.
func Render(list []Skill) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range list {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "- %s: %s", s.Name, s.Summary)
	}
	return b.String()
}

// allowedExt reports whether ext (lowercased, dot-prefixed) is a skill file type.
func allowedExt(ext string) bool { return extRank(ext) >= 0 }

// extRank returns ext's index in exts (its Load preference), or -1 when ext is
// not a skill file type.
func extRank(ext string) int {
	for i, e := range exts {
		if ext == e {
			return i
		}
	}
	return -1
}

// safeName rejects names that could escape the skills directory.
func safeName(name string) bool {
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return name == filepath.Base(name)
}

// readSummary returns the first non-empty line of the file with leading Markdown
// heading markers stripped and length bounded, or "" when there is none.
func readSummary(path string) string {
	f, err := os.Open(path)
	if err != nil {
		debug.Warn("tool", "skill file unreadable", "path", path, "err", err)
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), maxSummaryScan)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(sc.Text()), "#"))
		if line == "" {
			continue
		}
		return truncate(line, maxSummaryLen)
	}
	return ""
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
