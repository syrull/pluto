package session

import (
	"strings"
	"sync"
	"testing"
)

func TestBlackboardAppendAndFactsBy(t *testing.T) {
	b := NewBlackboard()
	if f, ok := b.Append("w1", FactHost, "10.0.0.1", "up"); !ok || f.Seq != 1 {
		t.Fatalf("first append = %+v ok=%v, want seq 1", f, ok)
	}
	if _, ok := b.Append("w1", FactService, "22/ssh", ""); !ok {
		t.Fatal("second append should succeed")
	}
	if _, ok := b.Append("w2", FactVuln, "CVE-2024-1", ""); !ok {
		t.Fatal("third append should succeed")
	}
	if _, ok := b.Append("w1", FactHost, "   ", ""); ok {
		t.Fatal("blank value must be dropped")
	}

	if got := len(b.Facts()); got != 3 {
		t.Fatalf("Facts len = %d, want 3", got)
	}
	if got := len(b.FactsBy("w1")); got != 2 {
		t.Fatalf("FactsBy(w1) len = %d, want 2", got)
	}
	if got := len(b.FactsBy("w2")); got != 1 {
		t.Fatalf("FactsBy(w2) len = %d, want 1", got)
	}
}

func TestBlackboardUnknownKindBecomesNote(t *testing.T) {
	b := NewBlackboard()
	f, _ := b.Append("w1", "wibble", "something", "")
	if f.Kind != FactNote {
		t.Fatalf("unknown kind = %q, want %q", f.Kind, FactNote)
	}
}

func TestBlackboardStateDedupesByValue(t *testing.T) {
	b := NewBlackboard()
	b.Append("w1", FactFlag, "CTF{abc}", "")
	b.Append("w2", FactFlag, "CTF{abc}", "duplicate report")
	b.Append("w2", FactFlag, "CTF{def}", "")
	b.Append("w1", FactCred, "root:toor", "")

	st := b.State()
	if len(st.Flags) != 2 {
		t.Fatalf("flags = %d, want 2 (deduped)", len(st.Flags))
	}
	if st.Flags[0].Value != "CTF{abc}" || st.Flags[0].Worker != "w1" {
		t.Fatalf("first flag = %+v, want the earliest (w1) report kept", st.Flags[0])
	}
	if len(st.Creds) != 1 {
		t.Fatalf("creds = %d, want 1", len(st.Creds))
	}
}

func TestBlackboardLeases(t *testing.T) {
	b := NewBlackboard()
	if !b.Claim("w1", "host:10.0.0.1") {
		t.Fatal("w1 first claim should be granted")
	}
	if b.Claim("w2", "host:10.0.0.1") {
		t.Fatal("w2 must not claim a unit w1 holds")
	}
	if !b.Claim("w1", "host:10.0.0.1") {
		t.Fatal("w1 re-claiming its own lease should be idempotently granted")
	}
	if got := b.Leases()["host:10.0.0.1"]; got != "w1" {
		t.Fatalf("holder = %q, want w1", got)
	}

	b.Release("w1", "host:10.0.0.1")
	if _, held := b.Leases()["host:10.0.0.1"]; held {
		t.Fatal("lease should be free after release")
	}
	if !b.Claim("w2", "host:10.0.0.1") {
		t.Fatal("w2 should claim the freed unit")
	}
}

func TestBlackboardJSONL(t *testing.T) {
	b := NewBlackboard()
	b.Append("w1", FactHost, "10.0.0.1", "")
	b.Append("w1", FactFlag, "CTF{x}", "")
	out := b.JSONL()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("JSONL lines = %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], `"kind":"host"`) {
		t.Fatalf("first JSONL line missing host kind: %s", lines[0])
	}
}

// TestBlackboardConcurrentAppend must be race-free under `go test -race`: many
// goroutines append at once, exercising the append-only design's freedom from
// write contention.
func TestBlackboardConcurrentAppend(t *testing.T) {
	b := NewBlackboard()
	const workers, per = 8, 50
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				b.Append("w", FactNote, strings.Repeat("x", id+1)+string(rune('a'+i%26)), "")
				_ = b.State()
				_ = b.Claim("w", "unit")
			}
		}(w)
	}
	wg.Wait()
	if got := len(b.Facts()); got != workers*per {
		t.Fatalf("Facts len = %d, want %d", got, workers*per)
	}
	// Sequence numbers must be unique and complete despite concurrent appends.
	seen := make(map[int]bool)
	for _, f := range b.Facts() {
		if seen[f.Seq] {
			t.Fatalf("duplicate seq %d", f.Seq)
		}
		seen[f.Seq] = true
	}
}
