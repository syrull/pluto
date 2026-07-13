package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractCodeBlocks(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want []codeBlock
	}{
		{
			name: "single fenced block with language",
			md:   "here you go:\n```go\nfmt.Println(\"hi\")\n```\ndone",
			want: []codeBlock{{lang: "go", code: "fmt.Println(\"hi\")"}},
		},
		{
			name: "multiple blocks in order",
			md:   "```sh\nls -la\n```\ntext\n```\nplain\n```",
			want: []codeBlock{{lang: "sh", code: "ls -la"}, {lang: "", code: "plain"}},
		},
		{
			name: "tilde fence",
			md:   "~~~python\nprint(1)\n~~~",
			want: []codeBlock{{lang: "python", code: "print(1)"}},
		},
		{
			name: "blank block skipped",
			md:   "```\n\n```",
			want: nil,
		},
		{
			name: "no fences",
			md:   "just prose with `inline` code",
			want: nil,
		},
		{
			name: "preserves inner blank lines and indentation",
			md:   "```go\nfunc f() {\n\n\treturn\n}\n```",
			want: []codeBlock{{lang: "go", code: "func f() {\n\n\treturn\n}"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractCodeBlocks(tc.md); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("extractCodeBlocks(%q) = %#v, want %#v", tc.md, got, tc.want)
			}
		})
	}
}

func TestCodeBlockTitle(t *testing.T) {
	if got := (codeBlock{lang: "go"}).title(); got != "go" {
		t.Errorf("title with lang = %q, want %q", got, "go")
	}
	if got := (codeBlock{}).title(); got != "code" {
		t.Errorf("title without lang = %q, want %q", got, "code")
	}
}

func TestFlushStreamRetainsCodeBlocks(t *testing.T) {
	m := model{md: newRenderer(80), width: 80}
	m.streamText = "sure:\n```go\nx := 1\n```\nand more\n```sh\necho hi\n```"
	m.flushStream()

	if len(m.codeBlocks) != 2 {
		t.Fatalf("expected 2 retained code blocks, got %d", len(m.codeBlocks))
	}
	if m.codeBlocks[0].code != "x := 1" || m.codeBlocks[1].code != "echo hi" {
		t.Fatalf("retained code = %#v", m.codeBlocks)
	}
	if b, ok := m.lastCode(); !ok || b.code != "echo hi" {
		t.Fatalf("lastCode() = %#v, %v; want the sh block", b, ok)
	}
	tr := m.transcript()
	if got := strings.Count(tr, "[ctrl+y]"); got != 2 {
		t.Fatalf("without mouse, transcript should carry one ctrl+y hint per block, got %d:\n%s", got, tr)
	}
}

func TestCopyButtonShownOnlyWithMouse(t *testing.T) {
	render := func(mouse bool) string {
		m := model{md: newRenderer(80), width: 80, mouse: mouse}
		m.streamText = "```go\nx := 1\n```"
		m.flushStream()
		return m.transcript()
	}
	if on := render(true); !strings.Contains(on, "Copy go") {
		t.Fatalf("with mouse on, a code block should show a Copy button:\n%s", on)
	}
	off := render(false)
	if !strings.Contains(off, "[ctrl+y] copy go") {
		t.Fatalf("without mouse, a code block should show the ctrl+y hint:\n%s", off)
	}
	if strings.Contains(off, "Copy go ▸") {
		t.Fatalf("without mouse, no clickable Copy button should be shown:\n%s", off)
	}
}

func TestExtractCodeBlockFourBacktickFence(t *testing.T) {
	// A block opened with four backticks holds a markdown sample that itself
	// contains a ``` fence; the language must not keep a stray backtick.
	md := "````markdown\n# Title\n```\ninner\n```\n````"
	got := extractCodeBlocks(md)
	if len(got) != 1 {
		t.Fatalf("expected 1 block, got %d: %#v", len(got), got)
	}
	if got[0].lang != "markdown" {
		t.Fatalf("lang = %q, want %q (no stray backtick)", got[0].lang, "markdown")
	}
	if got[0].code != "# Title\n```\ninner\n```" {
		t.Fatalf("code = %q, want the inner ``` fences preserved", got[0].code)
	}
}
