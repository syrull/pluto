// Package update self-updates the pluto binary from GitHub releases.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repo      = "syrull/pluto"
	userAgent = "pluto-updater"
)

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Run replaces the running binary with the latest GitHub release when it is
// newer than current. current is the build version ("dev" for local builds).
func Run(current string) error {
	ctx := context.Background()
	rel, err := latest(ctx)
	if err != nil {
		return err
	}
	if !newer(current, rel.TagName) {
		fmt.Printf("pluto is up to date (%s)\n", current)
		return nil
	}
	name := assetName()
	var url string
	for _, a := range rel.Assets {
		if a.Name == name {
			url = a.URL
			break
		}
	}
	if url == "" {
		return fmt.Errorf("release %s has no asset %q", rel.TagName, name)
	}
	fmt.Printf("updating pluto %s → %s\n", current, rel.TagName)
	if err := replace(ctx, url); err != nil {
		return err
	}
	fmt.Printf("updated to %s\n", rel.TagName)
	return nil
}

func assetName() string {
	return fmt.Sprintf("pluto_%s_%s", runtime.GOOS, runtime.GOARCH)
}

func latest(ctx context.Context) (*release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("query latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query latest release: %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no released version found")
	}
	return &rel, nil
}

// replace downloads url and atomically swaps it in for the current executable.
func replace(ctx context.Context, url string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".pluto-update-*")
	if err != nil {
		return fmt.Errorf("create temp file (need write access to %s): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("write update: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return fmt.Errorf("replace %s: %w", exe, err)
	}
	return nil
}

// newer reports whether latest is a higher semantic version than current. A
// "dev" or otherwise unparseable current always updates.
func newer(current, latest string) bool {
	c, okC := parse(current)
	l, okL := parse(latest)
	if !okL {
		return false
	}
	if !okC {
		return true
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
