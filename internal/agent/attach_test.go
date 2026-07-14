package agent

import (
	"context"
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func TestRunRecordsAttachments(t *testing.T) {
	a := New(llm.Stub{}, tool.NewRegistry(), "")
	atts := []llm.Attachment{{Kind: llm.AttachmentImage, MediaType: "image/png", Data: []byte{1, 2, 3}}}

	if _, err := a.Run(context.Background(), "look", atts, func(Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	idx := findUser(a.transcript, "look")
	if idx < 0 {
		t.Fatalf("user turn not recorded; roles=%v", roles(a.transcript))
	}
	if got := a.transcript[idx].Attachments; len(got) != 1 || got[0].MediaType != "image/png" {
		t.Fatalf("attachments on user turn = %+v, want one image/png", got)
	}
}

func TestSteerCarriesAttachments(t *testing.T) {
	a := New(llm.Stub{}, tool.NewRegistry(), "")
	atts := []llm.Attachment{{Kind: llm.AttachmentImage, MediaType: "image/jpeg", Data: []byte{9}}}

	a.Steer("with image", atts...)
	got := a.TakeSteering()
	if len(got) != 1 || got[0].Text != "with image" || len(got[0].Attachments) != 1 {
		t.Fatalf("Steer should queue text + attachments, got %+v", got)
	}
}

func TestEstimateTokensCountsAttachmentsNominally(t *testing.T) {
	base := estimateTokens([]llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	big := make([]byte, 4*1024*1024) // 4 MB of bytes must not be counted as text
	withImg := estimateTokens([]llm.Message{{
		Role: llm.RoleUser, Content: "hi",
		Attachments: []llm.Attachment{{Kind: llm.AttachmentImage, MediaType: "image/png", Data: big}},
	}})
	if got := withImg - base; got != imageTokenEstimate {
		t.Fatalf("attachment token delta = %d, want the nominal %d (not the raw byte size)", got, imageTokenEstimate)
	}
}
