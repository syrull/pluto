// Package diff computes and renders line-level diffs.
package diff

import "strings"

// MaxLines caps the diff computation to prevent worst-case time complexity.
const MaxLines = 2000

// Line is one line of a unified-style diff.
type Line struct {
	Op   byte
	Text string
}

// Result is the outcome of Compute: the diff Lines, or TooLarge when inputs exceed MaxLines.
type Result struct {
	Lines    []Line
	TooLarge bool
}

// Compute computes a line-level diff using Myers' O(ND) shortest-edit-script algorithm.
func Compute(old, new string) Result {
	a := SplitLines(old)
	b := SplitLines(new)

	if len(a) > MaxLines || len(b) > MaxLines {
		return Result{TooLarge: true}
	}
	return Result{Lines: myers(a, b)}
}

func myers(a, b []string) []Line {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}

	// V maps diagonal k (= x - y) to the furthest x reached on it. k ranges
	// over [-(n+m), n+m]; offset by max to index a flat slice. trace snapshots
	// V after each d so backtrack can replay the path.
	max := n + m
	v := make([]int, 2*max+1)
	offset := max
	var trace [][]int

	for d := 0; d <= max; d++ {
		vc := make([]int, len(v))
		copy(vc, v)
		trace = append(trace, vc)

		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
				x = v[k+1+offset]
			} else {
				x = v[k-1+offset] + 1
			}
			y := x - k

			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[k+offset] = x

			if x >= n && y >= m {
				return backtrack(a, b, trace, offset)
			}
		}
	}
	return nil // unreachable: a path is always found by d == max
}

func backtrack(a, b []string, trace [][]int, offset int) []Line {
	var out []Line

	x, y := len(a), len(b)
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y

		var prevK int
		if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[prevK+offset]
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			out = append(out, Line{Op: ' ', Text: a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				out = append(out, Line{Op: '+', Text: b[y-1]})
				y--
			} else {
				out = append(out, Line{Op: '-', Text: a[x-1]})
				x--
			}
		}
	}

	for i := 0; i < len(out)/2; i++ {
		out[i], out[len(out)-1-i] = out[len(out)-1-i], out[i]
	}
	return out
}

// SplitLines splits s into lines without trailing empty elements from final newlines.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Format renders lines as unified-style text with op prefix per line.
func Format(lines []Line) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteByte(l.Op)
		b.WriteString(l.Text)
	}
	return b.String()
}

// Stats counts added and removed lines in a diff.
func Stats(lines []Line) (added, removed int) {
	for _, dl := range lines {
		switch dl.Op {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}
