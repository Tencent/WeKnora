package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/youtube"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	youTubePlaylistListTimeout = 2 * time.Minute
	// youTubeDocWindowRunes bounds each transcript window sent to the chat
	// model so long videos fit typical context windows.
	youTubeDocWindowRunes = 24000
	youTubeDocMaxTokens   = 8192
)

// youTubeSource is the part of the YouTube client the knowledge service uses.
type youTubeSource interface {
	Playlist(ctx context.Context, playlistID string, limit int) (*youtube.Playlist, error)
	FetchTranscript(ctx context.Context, videoID string, transcribe youtube.Transcriber) (*youtube.Transcript, error)
	MaxVideosPerImport() int
}

var (
	defaultYouTubeSourceOnce sync.Once
	defaultYouTubeSource     youTubeSource
)

func (s *knowledgeService) youTube() youTubeSource {
	if s.youtubeSource != nil {
		return s.youtubeSource
	}
	defaultYouTubeSourceOnce.Do(func() {
		defaultYouTubeSource = youtube.NewClient(youtube.ConfigFromEnv())
	})
	return defaultYouTubeSource
}

// normalizeYouTubeImportURL rewrites a YouTube video link to its canonical
// watch URL so the same video is deduplicated however it was linked. It
// reports whether the link was a YouTube video, and rejects playlist links,
// which fan out into many knowledge entries through the YouTube batch endpoint.
func normalizeYouTubeImportURL(rawURL string) (string, bool, error) {
	link, ok := youtube.ParseURL(rawURL)
	if !ok {
		return rawURL, false, nil
	}
	if link.Kind == youtube.LinkPlaylist {
		return "", false, werrors.NewBadRequestError(
			"YouTube playlist links are imported with POST /knowledge-bases/{id}/knowledge/youtube")
	}
	return youtube.VideoURL(link.VideoID), true, nil
}

type youTubeImportVideo struct {
	id     string
	title  string
	folder string
}

// youTubePlaylistFolder names the folder a playlist's videos are imported
// into: the playlist title (or its ID when untitled) under baseFolder. Slashes
// in the title are replaced so a title never creates nested folders.
func youTubePlaylistFolder(baseFolder, title, playlistID string) string {
	name := strings.TrimSpace(strings.NewReplacer("/", "-", "\\", "-").Replace(title))
	if name == "" {
		name = playlistID
	}
	return types.NormalizeKnowledgeFolderPath(baseFolder + "/" + name)
}

// CreateKnowledgeFromYouTube queues one URL knowledge entry per distinct video
// across a batch of YouTube video and playlist links. Single videos are placed
// in folderPath and each playlist's videos in a subfolder named after the
// playlist. Links that cannot be resolved and videos that fail to queue are
// reported per item, and videos already in the knowledge base are reported as
// duplicates, so one bad link never fails the whole batch.
func (s *knowledgeService) CreateKnowledgeFromYouTube(ctx context.Context,
	kbID string, rawURLs []string, enableMultimodel *bool, tagIDs []string, channel string,
	processOverrides *types.KnowledgeProcessOverrides, folderPath string,
) (*types.YouTubeImportResult, error) {
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if err := s.checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}

	result := &types.YouTubeImportResult{
		Playlists:  []types.YouTubeImportPlaylist{},
		Created:    []*types.Knowledge{},
		Duplicates: []types.YouTubeImportItem{},
		Failed:     []types.YouTubeImportItem{},
	}
	videos, err := s.resolveYouTubeVideos(ctx, rawURLs, types.NormalizeKnowledgeFolderPath(folderPath), result)
	if err != nil {
		return nil, err
	}
	result.TotalVideos = len(videos)
	if len(videos) == 0 {
		message := "no importable YouTube videos were found"
		if len(result.Failed) > 0 {
			message += ": " + result.Failed[0].Error
		}
		return nil, werrors.NewBadRequestError(message)
	}

	for _, video := range videos {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		videoURL := youtube.VideoURL(video.id)
		item := types.YouTubeImportItem{URL: videoURL, VideoID: video.id, Title: video.title}
		knowledge, err := s.createKnowledgeFromURL(
			ctx, kbID, videoURL, "", "", enableMultimodel, video.title, tagIDs, channel, processOverrides, video.folder,
		)
		var duplicate *types.DuplicateKnowledgeError
		switch {
		case errors.As(err, &duplicate):
			if duplicate.Knowledge != nil {
				item.KnowledgeID = duplicate.Knowledge.ID
			}
			result.Duplicates = append(result.Duplicates, item)
		case err != nil:
			item.Error = err.Error()
			result.Failed = append(result.Failed, item)
		default:
			result.Created = append(result.Created, knowledge)
		}
	}
	logger.Infof(ctx, "[YouTube] import of %d link(s): %d created, %d duplicates, %d failed, truncated=%v",
		len(rawURLs), len(result.Created), len(result.Duplicates), len(result.Failed), result.Truncated)
	return result, nil
}

// resolveYouTubeVideos expands the submitted links into distinct videos, in
// submission order, capped at the client's per-import limit. A video linked
// more than once keeps the folder of its first occurrence. Unresolvable links
// are recorded in result.Failed; only a missing yt-dlp aborts the batch.
func (s *knowledgeService) resolveYouTubeVideos(
	ctx context.Context, rawURLs []string, baseFolder string, result *types.YouTubeImportResult,
) ([]youTubeImportVideo, error) {
	limit := s.youTube().MaxVideosPerImport()
	seen := make(map[string]bool)
	var videos []youTubeImportVideo
	add := func(id, title, folder string) {
		if seen[id] {
			return
		}
		if len(videos) >= limit {
			result.Truncated = true
			return
		}
		seen[id] = true
		videos = append(videos, youTubeImportVideo{id: id, title: title, folder: folder})
	}

	for _, rawURL := range rawURLs {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}
		link, ok := youtube.ParseURL(rawURL)
		if !ok {
			result.Failed = append(result.Failed, types.YouTubeImportItem{
				URL: rawURL, Error: "not a YouTube video or playlist link",
			})
			continue
		}
		if link.Kind == youtube.LinkVideo {
			add(link.VideoID, "", baseFolder)
			continue
		}
		// A playlist whose videos all overlap earlier links still fits once
		// the cap is reached, so list it with the full limit and let add()
		// enforce the cap on distinct videos.
		listCtx, cancel := context.WithTimeout(ctx, youTubePlaylistListTimeout)
		playlist, err := s.youTube().Playlist(listCtx, link.PlaylistID, limit)
		cancel()
		if err != nil {
			logger.Errorf(ctx, "[YouTube] failed to list playlist %s: %v", link.PlaylistID, err)
			if errors.Is(err, youtube.ErrToolMissing) {
				return nil, werrors.NewInternalServerError(err.Error())
			}
			result.Failed = append(result.Failed, types.YouTubeImportItem{
				URL: rawURL, Error: fmt.Sprintf("failed to read YouTube playlist: %v", err),
			})
			continue
		}
		before := len(videos)
		folder := youTubePlaylistFolder(baseFolder, playlist.Title, link.PlaylistID)
		for _, entry := range playlist.Entries {
			add(entry.VideoID, entry.Title, folder)
		}
		if playlist.Truncated {
			result.Truncated = true
		}
		if len(playlist.Entries) == 0 {
			result.Failed = append(result.Failed, types.YouTubeImportItem{
				URL: rawURL, Error: "the YouTube playlist has no importable videos",
			})
		}
		result.Playlists = append(result.Playlists, types.YouTubeImportPlaylist{
			URL: rawURL, PlaylistID: link.PlaylistID, Title: playlist.Title, FolderPath: folder,
			Videos: len(videos) - before,
		})
	}
	return videos, nil
}

// convertYouTube is the YouTube counterpart of convert: it fetches the
// video's transcript (captions, or ASR over the downloaded audio), asks the
// knowledge base's chat model to turn it into documentation, and returns the
// rendered Markdown for chunking. A nil result with nil error means the
// knowledge was already marked failed.
func (s *knowledgeService) convertYouTube(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	eff types.EffectiveProcessConfig,
	isLastRetry bool,
	videoID string,
) (*types.ReadResult, error) {
	s.beginStage(ctx, knowledge.ID, types.StageDocReader, types.JSONMap{
		"url": payload.URL, "is_url": true, "source": "youtube", "video_id": videoID,
	})

	var transcribe youtube.Transcriber
	if eff.ASRConfig.IsASREnabled() {
		transcribe = s.youTubeTranscriber(eff.ASRConfig.ModelID)
	}

	// No per-call cap like DocReaderCallTimeout: speech recognition over a long
	// video legitimately takes many sequential ASR calls. The document:process
	// task deadline (WEKNORA_DOCUMENT_PROCESS_TIMEOUT, default 2h) bounds it.
	transcript, err := s.youTube().FetchTranscript(ctx, videoID, transcribe)
	if err != nil {
		// Missing captions without ASR, or missing tools, will not fix
		// themselves on retry: fail immediately with an actionable message.
		if errors.Is(err, youtube.ErrNoTranscript) || errors.Is(err, youtube.ErrToolMissing) {
			knowledge.ParseStatus = types.ParseStatusFailed
			knowledge.ErrorMessage = err.Error()
			knowledge.UpdatedAt = time.Now()
			if updateErr := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); updateErr != nil {
				logger.Errorf(ctx, "failed to persist YouTube import error for %s: %v", knowledge.ID, updateErr)
			}
			s.failStage(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, err.Error(), nil)
			return nil, nil
		}
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			werrors.ErrCodeDocReaderParseFailed, "YouTube transcript fetch failed", err)
		return s.failKnowledge(ctx, knowledge, isLastRetry, "YouTube transcript fetch failed: %v", err)
	}
	if len(transcript.Segments) == 0 {
		transcript.Segments = []youtube.Segment{{Text: "[No speech detected in this video]"}}
	}

	documentation := s.generateYouTubeDocumentation(ctx, kb, transcript)
	markdown := youtube.RenderMarkdown(transcript, documentation)

	s.endStage(ctx, knowledge.ID, types.StageDocReader, types.JSONMap{
		"text_length":       len(markdown),
		"transcript_source": string(transcript.Source),
		"transcript_lang":   transcript.Language,
		"documentation":     documentation != "",
	})
	metadata := map[string]string{
		"title":             transcript.Video.Title,
		"source":            "youtube",
		"youtube_video_id":  videoID,
		"transcript_source": string(transcript.Source),
	}
	return &types.ReadResult{MarkdownContent: markdown, Metadata: metadata}, nil
}

// youTubeTranscriber adapts the knowledge base's ASR model to the YouTube
// client's per-audio-piece transcription callback.
func (s *knowledgeService) youTubeTranscriber(modelID string) youtube.Transcriber {
	return func(ctx context.Context, audio []byte, fileName string) ([]youtube.Segment, error) {
		asrModel, err := s.modelService.GetASRModel(ctx, modelID)
		if err != nil {
			return nil, fmt.Errorf("get ASR model: %w", err)
		}
		result, err := asrModel.Transcribe(ctx, audio, fileName)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		if len(result.Segments) == 0 {
			if text := strings.TrimSpace(result.Text); text != "" {
				return []youtube.Segment{{Text: text}}, nil
			}
			return nil, nil
		}
		segments := make([]youtube.Segment, 0, len(result.Segments))
		for _, seg := range result.Segments {
			if text := strings.TrimSpace(seg.Text); text != "" {
				segments = append(segments, youtube.Segment{Start: seg.Start, Text: text})
			}
		}
		return segments, nil
	}
}

const youTubeDocumentationPrompt = `You convert video transcripts into clear, well-structured documentation.

Rewrite the transcript excerpt you are given as Markdown documentation written in {{language}}:
- Organise the content under "###" and "####" headings (never "#" or "##").
- Keep every concrete fact, definition, step, command, code snippet, number and recommendation.
  Do not invent anything that is not in the transcript.
- Turn procedures into numbered steps and use lists or tables where they aid clarity.
- Drop filler, greetings, sponsor messages and verbal tics.
- The transcript lines start with [m:ss] timestamps; cite the timestamp where each section's material
  starts, e.g. "(from 12:34)".
- Output only the documentation, with no preamble or closing remarks.`

// generateYouTubeDocumentation turns a transcript into structured Markdown
// with the knowledge base's summary model. It is best-effort: without a
// model, or when the model fails, the document keeps only the transcript.
func (s *knowledgeService) generateYouTubeDocumentation(
	ctx context.Context, kb *types.KnowledgeBase, transcript *youtube.Transcript,
) string {
	if kb.SummaryModelID == "" {
		logger.Infof(ctx, "[YouTube] knowledge base %s has no summary model; storing transcript only", kb.ID)
		return ""
	}
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.Warnf(ctx, "[YouTube] failed to get chat model for documentation: %v", err)
		return ""
	}
	return writeYouTubeDocumentation(ctx, chatModel, transcript)
}

func writeYouTubeDocumentation(ctx context.Context, chatModel chat.Chat, transcript *youtube.Transcript) string {
	windows := youtube.SplitText(youtube.FormatTranscript(transcript.Segments, 60), youTubeDocWindowRunes)
	systemPrompt := types.RenderPromptPlaceholders(youTubeDocumentationPrompt, types.PlaceholderValues{
		"language": types.LanguageNameFromContext(ctx),
	})
	thinking := false
	modelCtx := types.WithLLMCallMetadata(ctx, "youtube_documentation", "")

	sections := make([]string, 0, len(windows))
	for i, window := range windows {
		header := fmt.Sprintf("Video title: %s\n", transcript.Video.Title)
		if len(windows) > 1 {
			header += fmt.Sprintf("Transcript part %d of %d. Document only this part.\n", i+1, len(windows))
		}
		response, err := chatModel.Chat(modelCtx, []chat.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: header + "\nTranscript:\n" + window},
		}, &chat.ChatOptions{Temperature: 0.2, MaxTokens: youTubeDocMaxTokens, Thinking: &thinking})
		if err != nil {
			logger.Warnf(ctx, "[YouTube] documentation generation failed for %s part %d/%d: %v",
				transcript.Video.ID, i+1, len(windows), err)
			return ""
		}
		content, err := validateSummaryOutput(response)
		if err != nil {
			logger.Warnf(ctx, "[YouTube] documentation for %s part %d/%d was unusable: %v",
				transcript.Video.ID, i+1, len(windows), err)
			return ""
		}
		sections = append(sections, content)
	}
	return strings.Join(sections, "\n\n")
}
