// Package skills discovers on-demand Agent Skills stored under a skills/
// directory. Each skill is a self-contained folder holding a SKILL.md file with
// YAML frontmatter (name + description) followed by Markdown instructions,
// mirroring the open Agent Skills standard. Only a compact index (name +
// description) rides in the system prompt; a skill's full body is loaded lazily
// via the skill tool, keeping the always-on prompt small (progressive
// disclosure).
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syrull/pluto/internal/debug"
)

// DirName is the conventional directory, relative to the working directory,
// that holds skill folders.
const DirName = "skills"

// FileName is the metadata + instructions file inside each skill folder.
const FileName = "SKILL.md"

// maxSummaryLen bounds an indexed description; it matches the Agent Skills
// standard's own description length limit so detailed trigger text survives.
const maxSummaryLen = 1024

// Skill is a discovered skill's index entry: its name (the folder name) and the
// description drawn from SKILL.md frontmatter that the model uses to decide when
// to load the full body.
type Skill struct {
	Name    string
	Summary string
}

// List discovers skills under dir, sorted by name. It is best-effort: a missing
// or unreadable directory yields nil, and folders whose SKILL.md is missing,
// unreadable, or carries no description are skipped since they can't be
// triggered or usefully listed.
func List(dir string) []Skill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			debug.Warn("tool", "skills dir unreadable", "dir", dir, "err", err)
		}
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if s, ok := index(dir, e.Name()); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// index reads a skill folder's SKILL.md and derives its index entry. ok is false
// when the file is missing/unreadable or has no description.
func index(dir, name string) (Skill, bool) {
	path := filepath.Join(dir, name, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			debug.Warn("tool", "skill file unreadable", "path", path, "err", err)
		}
		return Skill{}, false
	}
	meta, _ := parse(string(data))
	if n := meta["name"]; n != "" && n != name {
		debug.Debug("tool", "skill name mismatch; using folder name", "folder", name, "frontmatter", n)
	}
	desc := oneLine(meta["description"])
	if desc == "" {
		debug.Debug("tool", "skill has no description; skipped", "name", name, "path", path)
		return Skill{}, false
	}
	return Skill{Name: name, Summary: truncate(desc, maxSummaryLen)}, true
}

// Load returns the Markdown body (instructions) of the named skill under dir,
// with the YAML frontmatter stripped since its fields already ride in the index.
// It errors on an unknown, empty, or unsafe name.
func Load(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("skills: name is required")
	}
	if !safeName(name) {
		return "", fmt.Errorf("skills: invalid skill name %q", name)
	}
	path := filepath.Join(dir, name, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("skills: skill %q not found", name)
		}
		debug.Warn("tool", "skill file unreadable", "path", path, "err", err)
		return "", fmt.Errorf("skills: skill %q: %w", name, err)
	}
	_, body := parse(string(data))
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("skills: skill %q is empty", name)
	}
	return body, nil
}

// Render formats skills as compact "- name: description" lines for the always-on
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

// parse splits SKILL.md content into its YAML frontmatter scalar fields and the
// Markdown body. When content has no valid (terminated) leading frontmatter
// block, meta is nil and body is the full content unchanged.
func parse(content string) (meta map[string]string, body string) {
	s := strings.TrimPrefix(content, "\ufeff")
	first, after, ok := strings.Cut(s, "\n")
	if !ok || strings.TrimSpace(first) != "---" {
		return nil, content
	}
	meta = map[string]string{}
	rest := after
	for {
		line, next, found := strings.Cut(rest, "\n")
		if strings.TrimSpace(line) == "---" {
			return meta, next
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			k = strings.TrimSpace(k)
			v = unquote(strings.TrimSpace(v))
			if k != "" && v != "" {
				meta[k] = v
			}
		}
		if !found {
			return nil, content // unterminated frontmatter
		}
		rest = next
	}
}

// unquote strips a single pair of matching surrounding quotes from s.
func unquote(s string) string {
	if len(s) >= 2 {
		if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// oneLine collapses all runs of whitespace to single spaces so a multi-line or
// padded description renders as one compact index line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// safeName rejects names that could escape the skills directory.
func safeName(name string) bool {
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return name == filepath.Base(name)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
