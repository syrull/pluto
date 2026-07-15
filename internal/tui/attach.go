package tui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/syrull/pluto/internal/llm"
)

// maxAttachmentBytes caps a single image's size; Anthropic rejects images above
// ~5 MB, so we reject earlier with a clear message rather than a wire error.
const maxAttachmentBytes = 5 * 1024 * 1024

// supportedImageTypes is the set of media types the Messages API accepts as
// image input.
var supportedImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// loadImageAttachment reads an image file, validating its type and size, and
// returns it as an llm.Attachment. It rejects missing, empty, oversized, or
// non-image files with a clear error.
func loadImageAttachment(path string) (llm.Attachment, error) {
	clean := expandPath(strings.Trim(strings.TrimSpace(path), `"'`))
	if clean == "" {
		return llm.Attachment{}, fmt.Errorf("no image path given")
	}
	info, err := os.Stat(clean)
	if err != nil {
		return llm.Attachment{}, fmt.Errorf("cannot read %q: %w", path, err)
	}
	if info.IsDir() {
		return llm.Attachment{}, fmt.Errorf("%q is a directory, not an image", path)
	}
	if info.Size() > maxAttachmentBytes {
		return llm.Attachment{}, fmt.Errorf("%q is %s — over the %s image limit", filepath.Base(clean), humanBytes(int(info.Size())), humanBytes(maxAttachmentBytes))
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		return llm.Attachment{}, fmt.Errorf("cannot read %q: %w", path, err)
	}
	if len(data) == 0 {
		return llm.Attachment{}, fmt.Errorf("%q is empty", path)
	}
	mt := detectImageType(data)
	if !supportedImageTypes[mt] {
		return llm.Attachment{}, fmt.Errorf("%q is not a supported image (png, jpeg, gif, webp)", filepath.Base(clean))
	}
	return llm.Attachment{
		Kind:      llm.AttachmentImage,
		MediaType: mt,
		Data:      data,
		Name:      filepath.Base(clean),
	}, nil
}

// takeAttachments returns the staged attachments and clears them, so they ride
// exactly one turn.
func (m *model) takeAttachments() []llm.Attachment {
	atts := m.attachments
	m.attachments = nil
	return atts
}

// detectImageType sniffs the media type from the leading bytes, dropping any
// charset suffix so it can be matched against supportedImageTypes.
func detectImageType(data []byte) string {
	mt := http.DetectContentType(data)
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	return mt
}

// expandPath resolves a leading ~ to the user's home directory.
func expandPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// humanBytes renders a byte count compactly (e.g. 1536 → "1.5 KB").
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return trimZero(float64(n)/(1<<20)) + " MB"
	case n >= 1<<10:
		return trimZero(float64(n)/(1<<10)) + " KB"
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// attachmentChip renders a compact indicator of staged/sent image attachments,
// e.g. "📎 diagram.png" or "📎 2 images".
func attachmentChip(atts []llm.Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	if len(atts) == 1 {
		name := atts[0].Name
		if name == "" {
			name = "image"
		}
		return "📎 " + name
	}
	return fmt.Sprintf("📎 %d images", len(atts))
}
