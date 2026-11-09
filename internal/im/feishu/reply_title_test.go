package feishu

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func requireCardTitle(t *testing.T, raw, want string) {
	t.Helper()
	var card struct {
		Header struct {
			Title struct {
				Tag, Content string
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &card))
	require.Equal(t, "plain_text", card.Header.Title.Tag)
	require.Equal(t, want, card.Header.Title.Content)
}

func TestFeishuReplyTitleCards(t *testing.T) {
	for _, region := range []Region{RegionFeishu, RegionLark} {
		for _, title := range []string{"", "Network assistant", "光产品 <标题> & \"问答\""} {
			t.Run(region.Label+"/"+title, func(t *testing.T) {
				want := title
				if want == "" {
					want = types.DefaultFeishuReplyTitle
				}
				requireCardTitle(t, buildStreamingCardJSON(region, title), want)
			})
		}
	}
}
