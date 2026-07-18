package policy

import (
	"container/list"
	"strings"

	"github.com/syrull/pluto/internal/judge"
)

// defaultVerdictCacheCap bounds the per-process judge-verdict cache.
const defaultVerdictCacheCap = 256

// verdictCache is a bounded LRU of judge verdicts keyed by (normalized command,
// cwd). It is not internally synchronized; the owning ReviewGate guards every
// access with its mutex.
type verdictCache struct {
	cap int
	ll  *list.List // front = most-recently-used
	m   map[string]*list.Element
}

type verdictEntry struct {
	key     string
	verdict judge.Verdict
}

func newVerdictCache(capacity int) *verdictCache {
	if capacity < 1 {
		capacity = 1
	}
	return &verdictCache{cap: capacity, ll: list.New(), m: make(map[string]*list.Element, capacity)}
}

// get returns the cached verdict for key and marks it most-recently-used.
func (c *verdictCache) get(key string) (judge.Verdict, bool) {
	if c == nil {
		return judge.Verdict{}, false
	}
	el, ok := c.m[key]
	if !ok {
		return judge.Verdict{}, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*verdictEntry).verdict, true
}

// put stores v under key, evicting the least-recently-used entry when full.
func (c *verdictCache) put(key string, v judge.Verdict) {
	if c == nil {
		return
	}
	if el, ok := c.m[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*verdictEntry).verdict = v
		return
	}
	c.m[key] = c.ll.PushFront(&verdictEntry{key: key, verdict: v})
	if c.ll.Len() > c.cap {
		if oldest := c.ll.Back(); oldest != nil {
			c.ll.Remove(oldest)
			delete(c.m, oldest.Value.(*verdictEntry).key)
		}
	}
}

// verdictKey derives the cache key from the command and cwd. It deliberately
// excludes intent/why: the judge assesses what actually executes, and those
// model-supplied annotations must not be able to bust or poison the cache. The
// command is whitespace-normalized so cosmetic spacing differences share a hit.
func verdictKey(cmd, cwd string) string {
	return cwd + "\x00" + strings.Join(strings.Fields(cmd), " ")
}
