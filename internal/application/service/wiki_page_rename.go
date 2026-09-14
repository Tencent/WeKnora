package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var _ interfaces.WikiPageRenamer = (*wikiPageService)(nil)

func (s *wikiPageService) RenamePage(
	ctx context.Context, req interfaces.WikiPageRenameRequest,
) (*interfaces.WikiPageRenameResult, error) {
	renamer, ok := s.repo.(interfaces.WikiPageRenamer)
	if !ok {
		return nil, interfaces.ErrWikiRenameUnsupported
	}
	return renamer.RenamePage(ctx, req)
}
