// Package reposcan builds a compact, read-only snapshot of the working
// directory — layout, project type, git state, and README head — so the agent
// starts with the basic repo structure it would otherwise rediscover via
// ls/git/find on its opening turns. The snapshot is bounded so it stays cheap
// enough to ride inside the already-cached system prefix.
package reposcan

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxEntries     = 50
	maxReadmeLines = 20
	maxReadmeBytes = 1500
	maxOutputBytes = 4096
	gitTimeout     = 2 * time.Second
)

const header = "--- Repository snapshot (auto-detected at startup, read-only, may be stale) ---\n" +
	"Prefer this over re-running ls / git status / find to learn basic layout; " +
	"verify with tools only if a detail looks out of date."

// Overview scans the current working directory, honoring PLUTO_REPO_SCAN
// (0/false/off/no disables it). It returns "" when disabled or unreadable.
func Overview() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLUTO_REPO_SCAN"))) {
	case "0", "false", "off", "no":
		return ""
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return Scan(wd)
}

// Scan builds the snapshot for root. It is best-effort: an unreadable piece is
// skipped rather than failing the whole scan. It returns "" only when root
// itself can't be listed.
func Scan(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "\nWorking directory: %s", root)
	if t := projectTypes(root); t != "" {
		fmt.Fprintf(&b, "\nProject type: %s", t)
	}
	if g := gitSummary(root); g != "" {
		fmt.Fprintf(&b, "\nGit: %s", g)
	}
	if l := listing(entries); l != "" {
		fmt.Fprintf(&b, "\nTop level:\n%s", l)
	}
	if r := readmeHead(root, entries); r != "" {
		fmt.Fprintf(&b, "\nREADME (first lines):\n%s", r)
	}
	return bound(b.String())
}

// projectMarkers maps a marker file to a human label, in detection order.
var projectMarkers = []struct{ file, label string }{
	{"go.mod", "Go module"},
	{"package.json", "Node.js / JavaScript"},
	{"Cargo.toml", "Rust (Cargo)"},
	{"pyproject.toml", "Python"},
	{"setup.py", "Python"},
	{"requirements.txt", "Python"},
	{"pom.xml", "Java (Maven)"},
	{"build.gradle", "Java/Kotlin (Gradle)"},
	{"Gemfile", "Ruby"},
	{"composer.json", "PHP (Composer)"},
	{"CMakeLists.txt", "C/C++ (CMake)"},
}

func projectTypes(root string) string {
	var out []string
	seen := map[string]bool{}
	for _, m := range projectMarkers {
		if !fileExists(filepath.Join(root, m.file)) {
			continue
		}
		label := m.label
		if m.file == "go.mod" {
			if mod := goModulePath(filepath.Join(root, m.file)); mod != "" {
				label += " (" + mod + ")"
			}
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	return strings.Join(out, ", ")
}

// goModulePath returns the module path from a go.mod, or "".
func goModulePath(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func gitSummary(root string) string {
	if strings.TrimSpace(git(root, "rev-parse", "--is-inside-work-tree")) != "true" {
		return ""
	}
	var parts []string
	if br := strings.TrimSpace(git(root, "branch", "--show-current")); br != "" {
		parts = append(parts, "branch "+br)
	} else {
		parts = append(parts, "detached HEAD")
	}
	parts = append(parts, changeSummary(git(root, "status", "--porcelain")))
	if commit := strings.TrimSpace(git(root, "log", "-1", "--format=%h %s")); commit != "" {
		parts = append(parts, "last commit "+oneLine(commit, 72))
	}
	return strings.Join(parts, " · ")
}

func git(root string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// changeSummary condenses `git status --porcelain` into staged/modified/untracked counts.
func changeSummary(porcelain string) string {
	var staged, modified, untracked int
	for _, line := range strings.Split(strings.TrimRight(porcelain, "\n"), "\n") {
		if len(line) < 2 {
			continue
		}
		x, y := line[0], line[1]
		switch {
		case x == '?' && y == '?':
			untracked++
		default:
			if x != ' ' {
				staged++
			}
			if y != ' ' {
				modified++
			}
		}
	}
	var parts []string
	if staged > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", staged))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", modified))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", untracked))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}

// listing renders top-level entries, directories first, .git and .DS_Store
// omitted, capped to maxEntries with an overflow note.
func listing(entries []os.DirEntry) string {
	var dirs, files []string
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == ".DS_Store" {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name+"/")
		} else {
			files = append(files, name)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	all := append(dirs, files...)
	if len(all) == 0 {
		return ""
	}
	overflow := 0
	if len(all) > maxEntries {
		overflow = len(all) - maxEntries
		all = all[:maxEntries]
	}
	s := "  " + strings.Join(all, "  ")
	if overflow > 0 {
		s += fmt.Sprintf("  … (+%d more)", overflow)
	}
	return s
}

var readmeNames = map[string]bool{
	"readme.md": true, "readme": true, "readme.txt": true, "readme.rst": true,
}

// readmeHead returns the first bounded lines of a top-level README, or "".
func readmeHead(root string, entries []os.DirEntry) string {
	name := ""
	for _, e := range entries {
		if !e.IsDir() && readmeNames[strings.ToLower(e.Name())] {
			name = e.Name()
			break
		}
	}
	if name == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return ""
	}
	defer f.Close()
	var lines []string
	total := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() && len(lines) < maxReadmeLines {
		line := sc.Text()
		if total += len(line) + 1; total > maxReadmeBytes {
			break
		}
		lines = append(lines, "  "+line)
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func oneLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func bound(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + "\n… (snapshot truncated)"
}
