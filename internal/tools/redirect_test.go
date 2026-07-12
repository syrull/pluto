package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRedirectBash(t *testing.T) {
	// Get the project root (parent of internal directory)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}

	// Find the project root by looking for go.mod
	projectRoot := wd
	for {
		if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(projectRoot)
		if parent == projectRoot {
			t.Fatalf("could not find project root")
		}
		projectRoot = parent
	}

	// Prepare file paths for tests
	catFile := filepath.Join(projectRoot, "internal", "tools", "read.go")

	tests := []struct {
		name          string
		command       string
		expectHandled bool
		expectErr     bool
	}{
		// cat cases - use absolute path to a file that exists
		{
			name:          "cat single file",
			command:       "cat " + catFile,
			expectHandled: true,
			expectErr:     false,
		},
		{
			name:          "cat with flag",
			command:       "cat -n " + catFile,
			expectHandled: false,
		},
		{
			name:          "cat multiple operands",
			command:       "cat a b",
			expectHandled: false,
		},
		{
			name:          "cat with pipe",
			command:       "cat a.txt | wc -l",
			expectHandled: false,
		},
		{
			name:          "cat with quote",
			command:       "cat \"a b.txt\"",
			expectHandled: false,
		},
		{
			name:          "cat with expansion",
			command:       "cat $FILE",
			expectHandled: false,
		},

		// grep cases - use internal directory which exists in the project
		{
			name:          "grep no path (stdin)",
			command:       "grep foo",
			expectHandled: false,
		},
		{
			name:          "grep with pattern and path",
			command:       "grep package internal",
			expectHandled: true,
			expectErr:     false,
		},
		{
			name:          "grep with -r flag",
			command:       "grep -r package internal",
			expectHandled: true,
			expectErr:     false,
		},
		{
			name:          "grep with unknown flag",
			command:       "grep -x foo internal",
			expectHandled: false,
		},
		{
			name:          "grep with multiple acceptable flags",
			command:       "grep -rn package internal",
			expectHandled: true,
			expectErr:     false,
		},

		// rg cases - use internal directory
		{
			name:          "rg with pattern only",
			command:       "rg package",
			expectHandled: true,
			expectErr:     false,
		},
		{
			name:          "rg with pattern and path",
			command:       "rg package internal",
			expectHandled: true,
			expectErr:     false,
		},

		// unknown command cases
		{
			name:          "unknown command echo",
			command:       "echo hi",
			expectHandled: false,
		},
		{
			name:          "empty command",
			command:       "",
			expectHandled: false,
		},
		{
			name:          "ls command",
			command:       "ls internal",
			expectHandled: false,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err, handled := redirectBash(ctx, tt.command)

			// Check handled boolean
			if handled != tt.expectHandled {
				t.Errorf("handled = %v, want %v", handled, tt.expectHandled)
			}

			// For handled=true cases, verify err is nil (successful execution)
			if tt.expectHandled && tt.expectErr == false {
				if err != nil {
					t.Errorf("err = %v, want nil; out = %q", err, out)
				}
				if out == "" {
					t.Errorf("out is empty, expected content for handled=true case")
				}
			}

			// For handled=false cases, we don't care about out/err
			// (they would be handled by the fallback bash execution)
		})
	}
}
