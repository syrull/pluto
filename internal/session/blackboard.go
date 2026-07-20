package session

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/syrull/pluto/internal/debug"
)

// Fact kinds recorded on the blackboard. They map onto the structured result
// schema the orchestrator gathers (creds, footholds, vulns, flags, summary) plus
// the recon facts (host, service) and a free-form note.
const (
	FactHost     = "host"
	FactService  = "service"
	FactCred     = "cred"
	FactFoothold = "foothold"
	FactVuln     = "vuln"
	FactFlag     = "flag"
	FactNote     = "note"
	FactSummary  = "summary"
)

// factKinds is the set of recognized fact kinds; an unknown kind is coerced to
// FactNote so a worker can never wedge a malformed fact into the log.
var factKinds = map[string]bool{
	FactHost: true, FactService: true, FactCred: true, FactFoothold: true,
	FactVuln: true, FactFlag: true, FactNote: true, FactSummary: true,
}

// NormalizeKind maps an arbitrary kind string to a recognized fact kind,
// defaulting to FactNote. Exposed so the worker note tool validates identically.
func NormalizeKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	if factKinds[k] {
		return k
	}
	return FactNote
}

// Fact is one structured observation a worker appended to the blackboard. It is
// immutable once recorded; the append-only log is the source of truth and the
// reducer (State) folds it into current, deduplicated state.
type Fact struct {
	Seq    int       `json:"seq"`
	Time   time.Time `json:"time"`
	Worker string    `json:"worker"`
	Kind   string    `json:"kind"`
	Value  string    `json:"value"`
	Detail string    `json:"detail,omitempty"`
}

// leaseOp is one append-only lease mutation: a claim, or a release when Release
// is true. Folding the ops in order yields the current lease holder per unit.
type leaseOp struct {
	Time    time.Time `json:"time"`
	Worker  string    `json:"worker"`
	Unit    string    `json:"unit"`
	Release bool      `json:"release,omitempty"`
}

// State is the reduced view of the blackboard: facts grouped by kind and
// deduplicated by value (first writer wins), so the orchestrator sees each
// discovery once regardless of how many workers reported it.
type State struct {
	Hosts     []Fact `json:"hosts,omitempty"`
	Services  []Fact `json:"services,omitempty"`
	Creds     []Fact `json:"creds,omitempty"`
	Footholds []Fact `json:"footholds,omitempty"`
	Vulns     []Fact `json:"vulns,omitempty"`
	Flags     []Fact `json:"flags,omitempty"`
	Notes     []Fact `json:"notes,omitempty"`
	Summaries []Fact `json:"summaries,omitempty"`
}

// Blackboard is the shared, append-only coordination substrate for a fan-out of
// workers. Workers only ever append facts and lease ops, which sidesteps write
// contention between concurrent goroutines and leaves a replayable record for a
// post-hoc writeup. It is safe for concurrent use.
type Blackboard struct {
	mu       sync.RWMutex
	seq      int
	facts    []Fact
	byWorker map[string][]Fact // facts indexed by worker so FactsBy stays O(k)
	leases   []leaseOp
	now      func() time.Time // injectable clock for tests
}

// NewBlackboard returns an empty blackboard.
func NewBlackboard() *Blackboard {
	return &Blackboard{byWorker: make(map[string][]Fact), now: time.Now}
}

func (b *Blackboard) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Append records a fact from worker and returns it with its assigned sequence
// and timestamp. An unrecognized kind is coerced to a note. A blank value is
// dropped (returns the zero Fact and ok=false) so the log stays meaningful.
func (b *Blackboard) Append(worker, kind, value, detail string) (Fact, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		debug.Debug("worker", "blackboard append skipped (empty value)", "worker", worker, "kind", kind)
		return Fact{}, false
	}
	b.mu.Lock()
	b.seq++
	f := Fact{
		Seq:    b.seq,
		Time:   b.clock(),
		Worker: strings.TrimSpace(worker),
		Kind:   NormalizeKind(kind),
		Value:  value,
		Detail: strings.TrimSpace(detail),
	}
	b.facts = append(b.facts, f)
	if b.byWorker == nil {
		b.byWorker = make(map[string][]Fact)
	}
	b.byWorker[f.Worker] = append(b.byWorker[f.Worker], f)
	n := len(b.facts)
	b.mu.Unlock()
	debug.Info("worker", "blackboard fact", "seq", f.Seq, "worker", f.Worker,
		"kind", f.Kind, "value", truncateFact(f.Value, 200), "total", n)
	return f, true
}

// Facts returns a copy of every recorded fact in append order.
func (b *Blackboard) Facts() []Fact {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Fact, len(b.facts))
	copy(out, b.facts)
	return out
}

// FactsBy returns a copy of the facts appended by the named worker, in order.
// It reads a per-worker index so a poll stays O(k) in that worker's facts rather
// than scanning the whole log.
func (b *Blackboard) FactsBy(worker string) []Fact {
	worker = strings.TrimSpace(worker)
	b.mu.RLock()
	defer b.mu.RUnlock()
	src := b.byWorker[worker]
	if len(src) == 0 {
		return nil
	}
	out := make([]Fact, len(src))
	copy(out, src)
	return out
}

// State folds the append-only log into current, deduplicated state.
func (b *Blackboard) State() State {
	b.mu.RLock()
	facts := make([]Fact, len(b.facts))
	copy(facts, b.facts)
	b.mu.RUnlock()
	return reduce(facts)
}

// reduce groups facts by kind and drops duplicates by (kind, value), keeping the
// earliest report so the merged view is stable and order-independent per kind.
func reduce(facts []Fact) State {
	var st State
	seen := make(map[string]bool, len(facts))
	for _, f := range facts {
		key := f.Kind + "\x00" + f.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		switch f.Kind {
		case FactHost:
			st.Hosts = append(st.Hosts, f)
		case FactService:
			st.Services = append(st.Services, f)
		case FactCred:
			st.Creds = append(st.Creds, f)
		case FactFoothold:
			st.Footholds = append(st.Footholds, f)
		case FactVuln:
			st.Vulns = append(st.Vulns, f)
		case FactFlag:
			st.Flags = append(st.Flags, f)
		case FactSummary:
			st.Summaries = append(st.Summaries, f)
		default:
			st.Notes = append(st.Notes, f)
		}
	}
	return st
}

// Claim leases unit to worker so two workers don't race the same unit of work.
// It returns true when the lease is granted — either the unit was free or the
// caller already holds it (idempotent) — and false when another worker holds it.
// A granted claim is appended to the log.
func (b *Blackboard) Claim(worker, unit string) bool {
	worker, unit = strings.TrimSpace(worker), strings.TrimSpace(unit)
	if unit == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if holder, held := b.holderLocked(unit); held && holder != worker {
		debug.Info("worker", "lease denied", "worker", worker, "unit", truncateFact(unit, 120), "held_by", holder)
		return false
	}
	b.leases = append(b.leases, leaseOp{Time: b.clock(), Worker: worker, Unit: unit})
	debug.Info("worker", "lease granted", "worker", worker, "unit", truncateFact(unit, 120))
	return true
}

// Release drops worker's lease on unit (a no-op if it doesn't hold it). It is
// recorded as an append-only release op.
func (b *Blackboard) Release(worker, unit string) {
	worker, unit = strings.TrimSpace(worker), strings.TrimSpace(unit)
	if unit == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if holder, held := b.holderLocked(unit); !held || holder != worker {
		return
	}
	b.leases = append(b.leases, leaseOp{Time: b.clock(), Worker: worker, Unit: unit, Release: true})
	debug.Debug("worker", "lease released", "worker", worker, "unit", truncateFact(unit, 120))
}

// Leases returns the current unit→holder map, folded from the append-only ops.
func (b *Blackboard) Leases() map[string]string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]string)
	for _, op := range b.leases {
		if op.Release {
			delete(out, op.Unit)
			continue
		}
		out[op.Unit] = op.Worker
	}
	return out
}

// holderLocked returns the current holder of unit; the caller holds b.mu.
func (b *Blackboard) holderLocked(unit string) (string, bool) {
	holder := ""
	held := false
	for _, op := range b.leases {
		if op.Unit != unit {
			continue
		}
		if op.Release {
			holder, held = "", false
			continue
		}
		holder, held = op.Worker, true
	}
	return holder, held
}

// JSONL renders the fact log as newline-delimited JSON, one fact per line — a
// replayable action log for a post-hoc writeup.
func (b *Blackboard) JSONL() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var sb strings.Builder
	for _, f := range b.facts {
		line, err := json.Marshal(f)
		if err != nil {
			continue
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func truncateFact(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
