package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// DropImageReferenceActionName is the name a rule uses to select this action.
const DropImageReferenceActionName = "drop_image_reference"

// dropImageReferenceAction takes an image out of the text block that references
// it, without touching the image itself.
//
// The image file is left where it is, deliberately. It is the only copy of the
// bytes a document preview, a wiki page and a chat answer all render, so
// deleting it to tidy up one reference would break all three. Removing the
// reference is enough: an unreferenced image stops reaching the models through
// the body.
//
// NOTE — scope of this action (verified against the code, 2026-09-17): only the
// body reference is removed. The image's caption and OCR rows are NOT switched
// off or deleted; they stay enabled and keep participating in retrieval as
// standalone knowledge chunks. Dropping a reference is therefore not a way to
// silence an image entirely — to stop producing its caption or OCR text, turn
// the class's OCR (or the whole class) off in the image class policy table
// before ingestion instead.
type dropImageReferenceAction struct {
	chunkService interfaces.ChunkService
}

func newDropImageReferenceAction(chunkService interfaces.ChunkService) *dropImageReferenceAction {
	return &dropImageReferenceAction{chunkService: chunkService}
}

func (a *dropImageReferenceAction) Name() string { return DropImageReferenceActionName }

func (a *dropImageReferenceAction) Phase() ActionPhase { return ActionPhaseInline }

func (a *dropImageReferenceAction) Describe() ActionMeta {
	return ActionMeta{
		Name:  DropImageReferenceActionName,
		Title: "Drop the image reference",
		Description: "Removes the image's Markdown or HTML reference from its text block. " +
			"The image file, its caption row and its OCR row are all kept and remain searchable.",
		Phase: ActionPhaseInline,
	}
}

// Apply removes one image's reference from its parent block.
//
// A block that held nothing but that image cannot be emptied — the repository
// rejects empty content — so it is switched off instead. Either way the block
// handed in is kept in step with what was stored, because the same block may
// reference several images that each need dropping, and the next edit has to
// build on this one rather than on the copy read before it.
func (a *dropImageReferenceAction) Apply(ctx context.Context, req *ActionRequest) (*ActionResult, error) {
	if req == nil || req.Candidate == nil {
		return skippedImageResult("there is no image to act on"), nil
	}
	candidate := req.Candidate
	block := candidate.ParentChunk
	if block == nil {
		return skippedImageResult("the image has no parent text block"), nil
	}
	// Only text blocks are editable, and the repository enforces that. Checking
	// here turns a rejected call into a stated reason.
	if block.ChunkType != types.ChunkTypeText {
		return skippedImageResult("the parent block is not a text block"), nil
	}

	dropURLs := imageDropURLSet(candidate)
	if len(dropURLs) == 0 {
		return skippedImageResult("the image has no URL to match a reference on"), nil
	}

	detail := map[string]any{"image_url": candidate.URL}
	if candidate.Class != "" {
		detail["image_class"] = candidate.Class
	}

	newContent := searchutil.DropMarkdownImagesByURLs(block.Content, dropURLs)
	if newContent == block.Content {
		// The body does not reference this image. Some parsers attach images
		// rather than inlining them; editing the block anyway would record a
		// revision that changes nothing.
		detail["reason"] = "the text block does not reference this image"
		return &ActionResult{Outcome: ActionResultSkipped, Detail: detail}, nil
	}
	detail["reference_removed"] = true

	if req.DryRun {
		return &ActionResult{Outcome: ActionResultDryRun, Detail: detail}, nil
	}

	if strings.TrimSpace(newContent) == "" {
		detail["parent_disabled"] = true
		if err := a.setBlockEnabled(ctx, block, false); err != nil {
			return &ActionResult{Outcome: ActionResultFailed, Detail: detail}, err
		}
		return &ActionResult{Outcome: ActionResultApplied, Detail: detail}, nil
	}

	if err := a.replaceBlockContent(ctx, block, newContent); err != nil {
		return &ActionResult{Outcome: ActionResultFailed, Detail: detail}, err
	}
	return &ActionResult{Outcome: ActionResultApplied, Detail: detail}, nil
}

// imageDropURLSet is the set of URLs a body edit matches on. Both the current
// and the original URL go in, because the body may reference either one
// depending on whether the parser rewrote it on the way in.
func imageDropURLSet(candidate *ImageCandidate) map[string]bool {
	urls := make(map[string]bool, 2)
	if candidate.URL != "" {
		urls[candidate.URL] = true
	}
	if candidate.OriginalURL != "" {
		urls[candidate.OriginalURL] = true
	}
	return urls
}

func skippedImageResult(reason string) *ActionResult {
	return &ActionResult{Outcome: ActionResultSkipped, Detail: map[string]any{"reason": reason}}
}

func (a *dropImageReferenceAction) replaceBlockContent(ctx context.Context, block *types.Chunk, content string) error {
	service, err := a.requireChunkService()
	if err != nil {
		return err
	}
	// The revision is checked rather than assumed, so a concurrent edit is not
	// silently overwritten. This block was read moments ago, so a conflict means
	// somebody else is editing the document and this action should stand down.
	revision := block.ContentRevision
	updated, err := service.UpdateDocumentChunk(ctx, block.ID, &content, nil, &revision)
	if err != nil {
		return fmt.Errorf("drop image reference from chunk %s: %w", block.ID, err)
	}
	syncEditedBlock(block, updated)
	return nil
}

func (a *dropImageReferenceAction) setBlockEnabled(ctx context.Context, block *types.Chunk, enabled bool) error {
	service, err := a.requireChunkService()
	if err != nil {
		return err
	}
	// nil content asks for a status change only. It has to be this way round:
	// the repository rejects empty content outright, so a block whose images
	// were everything it had cannot be emptied, only retired.
	revision := block.ContentRevision
	updated, err := service.UpdateDocumentChunk(ctx, block.ID, nil, &enabled, &revision)
	if err != nil {
		return fmt.Errorf("disable chunk %s after dropping its images: %w", block.ID, err)
	}
	syncEditedBlock(block, updated)
	return nil
}

// syncEditedBlock copies the stored result back onto the caller's block, so the
// in-memory document keeps matching what was written. The revision especially:
// the next edit on this same block has to present the revision the last write
// produced, or the optimistic check would reject it.
func syncEditedBlock(block *types.Chunk, updated *types.Chunk) {
	if block == nil || updated == nil {
		return
	}
	block.Content = updated.Content
	block.IsEnabled = updated.IsEnabled
	block.ContentRevision = updated.ContentRevision
}

func (a *dropImageReferenceAction) requireChunkService() (interfaces.ChunkService, error) {
	if a.chunkService == nil {
		return nil, errors.New("drop_image_reference: no chunk service is configured")
	}
	return a.chunkService, nil
}
