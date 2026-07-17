// Package session persists conversations to disk so they can be resumed later.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
)

// formatVersion is the on-disk schema version, bumped on incompatible changes.
// v2 adds the multi-agent Agents/Active fields; v1 files (Messages only) still
// load and are treated as a single-agent session.
const formatVersion = 2

const ext = ".json"

// ErrNotFound is returned when a named session does not exist.
var ErrNotFound = errors.New("session: not found")

// Session is a persisted conversation and its metadata. A v2 session records a
// set of Agents; Messages holds the active agent's transcript so v1 readers and
// the listing count still work. Cwd is the working directory the conversation
// happened in, used to scope /resume to the current folder.
type Session struct {
	Version   int           `json:"version"`
	ID        string        `json:"id"`
	Title     string        `json:"title,omitempty"`
	Model     string        `json:"model,omitempty"`
	Cwd       string        `json:"cwd,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Messages  []llm.Message `json:"messages"`
	Agents    []Agent       `json:"agents,omitempty"`
	Active    int           `json:"active,omitempty"`
}

// Agent is one persisted conversation within a multi-agent session: its
// transcript plus the working directory and label that identify it. Goal holds an
// active /goal completion condition (omitted when there is none or it was already
// achieved); its turn/timer/token counters are not persisted — they reset on
// resume, matching upstream.
type Agent struct {
	Label    string        `json:"label,omitempty"`
	Cwd      string        `json:"cwd,omitempty"`
	Worktree bool          `json:"worktree,omitempty"`
	Goal     string        `json:"goal,omitempty"`
	Messages []llm.Message `json:"messages"`
}

// AgentList returns the session's agents, upgrading a v1 (Messages-only) session
// to a single agent so callers can treat every session uniformly.
func (s *Session) AgentList() []Agent {
	if len(s.Agents) > 0 {
		return s.Agents
	}
	return []Agent{{Messages: s.Messages}}
}

// Meta is lightweight session metadata for listing without loading transcripts.
type Meta struct {
	ID        string
	Title     string
	Model     string
	Cwd       string
	CreatedAt time.Time
	UpdatedAt time.Time
	Count     int
}

// Store reads and writes sessions as one JSON file per conversation.
type Store struct{ dir string }

// Dir returns the configured sessions directory: PLUTO_SESSIONS_DIR, or
// ~/.pluto/sessions by default.
func Dir() string {
	if d := strings.TrimSpace(os.Getenv("PLUTO_SESSIONS_DIR")); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".pluto", "sessions")
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// Open returns a Store rooted at Dir(), creating the directory if missing.
func Open() (*Store, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("session: create dir %q: %w", dir, err)
	}
	debug.Debug("session", "store opened", "dir", dir)
	return &Store{dir: dir}, nil
}

// Dir reports the directory the store writes to.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+ext) }

// Save writes sess atomically (temp file + rename). It stamps the format
// version and UpdatedAt, and CreatedAt when unset.
func (s *Store) Save(sess *Session) error {
	sess.ID = Sanitize(sess.ID)
	if sess.ID == "" {
		return errors.New("session: empty id")
	}
	now := time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	sess.UpdatedAt = now
	sess.Version = formatVersion

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, sess.ID+".*"+ext+".tmp")
	if err != nil {
		return fmt.Errorf("session: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("session: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("session: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path(sess.ID)); err != nil {
		return fmt.Errorf("session: rename: %w", err)
	}
	debug.Info("session", "saved", "id", sess.ID, "path", s.path(sess.ID),
		"cwd", sess.Cwd, "agents", len(sess.Agents), "messages", len(sess.Messages))
	return nil
}

// Load reads the session with the given id, returning ErrNotFound when it does
// not exist and a descriptive error for corrupt or unsupported files.
func (s *Store) Load(id string) (*Session, error) {
	id = Sanitize(id)
	if id == "" {
		return nil, ErrNotFound
	}
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		debug.Debug("session", "load miss", "id", id)
		return nil, ErrNotFound
	}
	if err != nil {
		debug.Warn("session", "read failed", "id", id, "err", err)
		return nil, fmt.Errorf("session: read %q: %w", id, err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		debug.Warn("session", "corrupt file", "id", id, "err", err)
		return nil, fmt.Errorf("session: %q is not a valid session file: %w", id, err)
	}
	if sess.Version == 0 || sess.Version > formatVersion {
		debug.Warn("session", "unsupported version", "id", id, "version", sess.Version)
		return nil, fmt.Errorf("session: %q has unsupported format version %d", id, sess.Version)
	}
	debug.Info("session", "loaded", "id", id, "version", sess.Version, "cwd", sess.Cwd,
		"agents", len(sess.Agents), "messages", len(sess.Messages), "active", sess.Active)
	return &sess, nil
}

// List returns metadata for every readable session, newest first. Unreadable or
// foreign files are skipped rather than failing the whole listing.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: read dir: %w", err)
	}
	var metas []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil || sess.Version == 0 {
			continue
		}
		metas = append(metas, Meta{
			ID: sess.ID, Title: sess.Title, Model: sess.Model, Cwd: sess.Cwd,
			CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt, Count: len(sess.Messages),
		})
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	debug.Debug("session", "listed", "count", len(metas))
	return metas, nil
}

// ListForCwd returns metadata for sessions recorded in cwd, newest first. Legacy
// sessions that recorded no path are always included so pre-scoping conversations
// are never hidden. An empty cwd returns every session (see List).
func (s *Store) ListForCwd(cwd string) ([]Meta, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	cwd = normalizeCwd(cwd)
	if cwd == "" {
		debug.Debug("session", "list for cwd", "cwd", "", "matched", len(all), "total", len(all))
		return all, nil
	}
	var metas []Meta
	var legacy int
	for _, m := range all {
		switch {
		case m.Cwd == "":
			legacy++
			metas = append(metas, m)
		case normalizeCwd(m.Cwd) == cwd:
			metas = append(metas, m)
		}
	}
	debug.Debug("session", "list for cwd", "cwd", cwd,
		"matched", len(metas), "legacy", legacy, "total", len(all))
	return metas, nil
}

func normalizeCwd(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return filepath.Clean(cwd)
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// Sanitize converts name into a safe, single-segment file stem: it strips any
// directory parts, replaces unsafe characters with '-', and trims separators so
// a session id can never escape the sessions directory.
func Sanitize(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = unsafeChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-._")
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// NewID builds a filesystem-safe session id from the current time and title.
func NewID(title string) string {
	stamp := time.Now().Format("20060102-150405")
	slug := Sanitize(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(title)), " ", "-"))
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-._")
	}
	if slug == "" {
		return stamp
	}
	return stamp + "-" + slug
}

// TitleFromMessages derives a short title from the first user message.
func TitleFromMessages(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			if t := truncateTitle(oneLine(m.Content)); t != "" {
				return t
			}
		}
	}
	return "untitled"
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func truncateTitle(s string) string {
	const max = 60
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
