package feishu

import "github.com/Tencent/WeKnora/internal/types"

// Open Platform API origins. Feishu and Lark are the same product deployed on
// two isolated clouds; the wiki/docx/drive APIs this connector uses are
// identical, only the host differs.
const (
	feishuOpenBaseURL = "https://open.feishu.cn"
	larkOpenBaseURL   = "https://open.larksuite.com"
)

// Web origins used to build human-facing links to wiki spaces and nodes. These
// are the end-user app hosts, not the API hosts.
const (
	feishuWebBaseURL = "https://feishu.cn"
	larkWebBaseURL   = "https://larksuite.com"
)

// Region selects which cloud the connector syncs from. Apps, tenants, tokens
// and document tokens are scoped to a single cloud and are never valid on the
// other, so a Region is fixed per connector instance at registration.
type Region struct {
	// ConnectorType is the types.ConnectorType* identifier this instance serves.
	ConnectorType string
	// OpenBaseURL is the Open Platform API origin, without a trailing slash.
	// It is only the default: a data source may override it via base_url.
	OpenBaseURL string
	// WebBaseURL is the app origin used to build wiki links shown to users.
	WebBaseURL string
	// Label prefixes log lines so operators can tell the clouds apart.
	Label string
}

var (
	// RegionFeishu is the Chinese mainland cloud (飞书).
	RegionFeishu = Region{
		ConnectorType: types.ConnectorTypeFeishu,
		OpenBaseURL:   feishuOpenBaseURL,
		WebBaseURL:    feishuWebBaseURL,
		Label:         "Feishu",
	}

	// RegionLark is the international cloud (Lark).
	RegionLark = Region{
		ConnectorType: types.ConnectorTypeLark,
		OpenBaseURL:   larkOpenBaseURL,
		WebBaseURL:    larkWebBaseURL,
		Label:         "Lark",
	}
)

// wikiURL builds the user-facing link to a wiki space or node on this cloud.
// The token is either a space_id or a node_token; both live under /wiki/.
func (r Region) wikiURL(token string) string {
	return r.WebBaseURL + "/wiki/" + token
}
