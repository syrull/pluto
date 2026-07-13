package tui

import "strings"

// codeBlock is a fenced code block extracted from assistant markdown, retained
// so its raw source can be copied to the clipboard.
type codeBlock struct {
	lang string
	code string
}

// title labels the block for affordance and status text.
func (b codeBlock) title() string {
	if b.lang == "" {
		return "code"
	}
	return b.lang
}

// mdSegment is a run of assistant markdown: either prose or a single fenced code
// block. code is set only when isCode is true; raw is the source to render, with
// the fence lines kept for code segments so glamour styles them as a block.
type mdSegment struct {
	code   codeBlock
	isCode bool
	raw    string
}

// splitMarkdown splits md into ordered prose and code-block segments so each
// fenced block can be rendered with its own copy affordance. A fence is a run of
// 3+ backticks or tildes at the start of a trimmed line; the closing fence must
// be at least as long, so blocks opened with 4+ backticks may contain literal
// ``` lines. Blank blocks and blank prose runs are dropped.
func splitMarkdown(md string) []mdSegment {
	var segs []mdSegment
	var prose, buf []string
	var open string
	var fenceChar byte
	var fenceLen int
	var lang string
	in := false

	flushProse := func() {
		if s := strings.Join(prose, "\n"); strings.TrimSpace(s) != "" {
			segs = append(segs, mdSegment{raw: s})
		}
		prose = nil
	}

	for _, ln := range strings.Split(md, "\n") {
		t := strings.TrimSpace(ln)
		if !in {
			if ch, n, info, ok := openFence(t); ok {
				flushProse()
				in, fenceChar, fenceLen, lang, open, buf = true, ch, n, info, ln, nil
				continue
			}
			prose = append(prose, ln)
			continue
		}
		if isCloseFence(t, fenceChar, fenceLen) {
			if code := strings.Join(buf, "\n"); strings.TrimSpace(code) != "" {
				raw := open + "\n" + code + "\n" + ln
				segs = append(segs, mdSegment{code: codeBlock{lang: lang, code: code}, isCode: true, raw: raw})
			}
			in = false
			continue
		}
		buf = append(buf, ln)
	}
	// An unterminated fence falls back to prose so its text is never dropped.
	if in {
		prose = append([]string{open}, buf...)
		flushProse()
	}
	flushProse()
	return segs
}

// extractCodeBlocks returns the fenced code blocks in md, in order.
func extractCodeBlocks(md string) []codeBlock {
	var blocks []codeBlock
	for _, seg := range splitMarkdown(md) {
		if seg.isCode {
			blocks = append(blocks, seg.code)
		}
	}
	return blocks
}

// openFence reports whether s opens a code fence, returning the fence character,
// its run length, and the language taken from the info string.
func openFence(s string) (ch byte, n int, lang string, ok bool) {
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return 0, 0, "", false
	}
	ch = s[0]
	n = runLen(s, ch)
	if n < 3 {
		return 0, 0, "", false
	}
	info := strings.TrimSpace(s[n:])
	// A backtick info string may not contain a backtick, so a line like ```` `x ````
	// is not an opening fence.
	if ch == '`' && strings.Contains(info, "`") {
		return 0, 0, "", false
	}
	if fields := strings.Fields(info); len(fields) > 0 {
		lang = fields[0]
	}
	return ch, n, lang, true
}

// isCloseFence reports whether s is a closing fence: only fence characters, at
// least as long as the opening run.
func isCloseFence(s string, ch byte, minLen int) bool {
	n := runLen(s, ch)
	return n >= minLen && n == len(s)
}

func runLen(s string, ch byte) int {
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	return n
}

// lastCode returns the most recently retained code block, if any.
func (m model) lastCode() (codeBlock, bool) {
	if len(m.codeBlocks) == 0 {
		return codeBlock{}, false
	}
	return m.codeBlocks[len(m.codeBlocks)-1], true
}

// codeAtScreen maps a screen row to the code block whose copy affordance is
// under it, if any.
func (m model) codeAtScreen(y int) (codeBlock, bool) {
	if !m.ready || y < 0 || y >= m.vp.Height() {
		return codeBlock{}, false
	}
	return m.codeAtContentLine(m.vp.YOffset() + y)
}

func (m model) codeAtContentLine(target int) (codeBlock, bool) {
	if e, ok := m.entryAtContentLine(target); ok && e.copyID > 0 && e.copyID <= len(m.codeBlocks) {
		return m.codeBlocks[e.copyID-1], true
	}
	return codeBlock{}, false
}
