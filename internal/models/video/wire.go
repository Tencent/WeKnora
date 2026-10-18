package video

// The request and response shapes of the video call. They are deliberately
// local to this package: the chat protocol's neutral message type has no video
// part, and adding one there would change the wire shape of every chat call for
// a single consumer.

// finishReasonLength is the vendor's "output budget exhausted" marker.
const finishReasonLength = "length"

// chatRequest is the OpenAI-compatible request body.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

// chatMessage is one turn. The video call sends a single user turn whose
// content is a list of typed parts.
type chatMessage struct {
	Role    string            `json:"role"`
	Content []chatContentPart `json:"content"`
}

// chatContentPart is either a text part or a video reference. Exactly one of
// Text and VideoURL is set, matching Type.
type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	VideoURL *chatVideoURL `json:"video_url,omitempty"`
}

// chatVideoURL carries the oss:// or http(s) reference of the video.
type chatVideoURL struct {
	URL string `json:"url"`
}

// chatResponse is the subset of the completion reply this package reads.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}
