package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedRead(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}

func TestReadFullFile(t *testing.T) {
	r := Read{}
	path := seedRead(t, "alpha\nbravo\ncharlie\n")
	args := json.RawMessage(`{"path":"` + path + `"}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	want := "1\talpha\n2\tbravo\n3\tcharlie\n"
	if result != want {
		t.Fatalf("Read.Execute() = %q, want %q", result, want)
	}
}

func TestReadOffset(t *testing.T) {
	r := Read{}
	path := seedRead(t, "alpha\nbravo\ncharlie\ndelta\n")
	args := json.RawMessage(`{"path":"` + path + `","offset":3}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	want := "3\tcharlie\n4\tdelta\n"
	if result != want {
		t.Fatalf("Read.Execute() = %q, want %q", result, want)
	}
}

func TestReadLimit(t *testing.T) {
	r := Read{}
	path := seedRead(t, "alpha\nbravo\ncharlie\ndelta\n")
	args := json.RawMessage(`{"path":"` + path + `","limit":2}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	want := "1\talpha\n2\tbravo\n"
	if result != want {
		t.Fatalf("Read.Execute() = %q, want %q", result, want)
	}
}

func TestReadOffsetAndLimit(t *testing.T) {
	r := Read{}
	path := seedRead(t, "alpha\nbravo\ncharlie\ndelta\necho\n")
	args := json.RawMessage(`{"path":"` + path + `","offset":2,"limit":2}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	want := "2\tbravo\n3\tcharlie\n"
	if result != want {
		t.Fatalf("Read.Execute() = %q, want %q", result, want)
	}
}

func TestReadOffsetPastEnd(t *testing.T) {
	r := Read{}
	path := seedRead(t, "alpha\nbravo\n")
	args := json.RawMessage(`{"path":"` + path + `","offset":10}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	if !strings.Contains(result, "past end") {
		t.Fatalf("Read.Execute() = %q, want message containing %q", result, "past end")
	}
}

func TestReadEmptyFile(t *testing.T) {
	r := Read{}
	path := seedRead(t, "")
	args := json.RawMessage(`{"path":"` + path + `"}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	if result != "(empty file)" {
		t.Fatalf("Read.Execute() = %q, want %q", result, "(empty file)")
	}
}

func TestReadByteCap(t *testing.T) {
	r := Read{}
	// Each line is well over 100 bytes; enough lines to exceed readMaxBytes.
	line := strings.Repeat("x", 200) + "\n"
	var sb strings.Builder
	for i := 0; i < 400; i++ {
		sb.WriteString(line)
	}
	path := seedRead(t, sb.String())
	args := json.RawMessage(`{"path":"` + path + `"}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	if len(result) > readMaxBytes+128 {
		t.Fatalf("Read.Execute() returned %d bytes, want <= %d", len(result), readMaxBytes+128)
	}
	if !strings.Contains(result, "truncated") {
		t.Fatalf("Read.Execute() missing truncation notice; got tail %q", result[len(result)-80:])
	}
}

func TestReadMissingFile(t *testing.T) {
	r := Read{}
	args := json.RawMessage(`{"path":"/nonexistent/path/xyz.txt"}`)
	if _, err := r.Execute(context.Background(), args); err == nil {
		t.Fatal("Read.Execute() error = nil, want error for missing file")
	}
}

func TestReadEmptyPath(t *testing.T) {
	r := Read{}
	args := json.RawMessage(`{"path":""}`)
	_, err := r.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("Read.Execute() error = %v, want 'path is required'", err)
	}
}

func TestReadNegativeOffset(t *testing.T) {
	r := Read{}
	path := seedRead(t, "alpha\n")
	args := json.RawMessage(`{"path":"` + path + `","offset":-1}`)
	_, err := r.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "offset must be >= 0") {
		t.Fatalf("Read.Execute() error = %v, want offset validation error", err)
	}
}

func TestReadInvalidJSON(t *testing.T) {
	r := Read{}
	args := json.RawMessage(`{"path":}`)
	if _, err := r.Execute(context.Background(), args); err == nil {
		t.Fatal("Read.Execute() error = nil, want error for invalid JSON")
	}
}

func TestReadNoTrailingNewline(t *testing.T) {
	r := Read{}
	path := seedRead(t, "alpha\nbravo")
	args := json.RawMessage(`{"path":"` + path + `"}`)
	result, err := r.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Read.Execute() error = %v, want nil", err)
	}
	want := "1\talpha\n2\tbravo\n"
	if result != want {
		t.Fatalf("Read.Execute() = %q, want %q", result, want)
	}
}
