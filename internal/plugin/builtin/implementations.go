package builtin

import (
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/datasource"
	confluenceConnector "github.com/Tencent/WeKnora/internal/datasource/connector/confluence"
	dingtalkConnector "github.com/Tencent/WeKnora/internal/datasource/connector/dingtalk"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/drive"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/wiki"
	gitlabConnector "github.com/Tencent/WeKnora/internal/datasource/connector/gitlab"
	imaConnector "github.com/Tencent/WeKnora/internal/datasource/connector/ima"
	notionConnector "github.com/Tencent/WeKnora/internal/datasource/connector/notion"
	rssConnector "github.com/Tencent/WeKnora/internal/datasource/connector/rss"
	yuqueConnector "github.com/Tencent/WeKnora/internal/datasource/connector/yuque"
	"github.com/Tencent/WeKnora/internal/im"
	imDingtalk "github.com/Tencent/WeKnora/internal/im/dingtalk"
	imFeishu "github.com/Tencent/WeKnora/internal/im/feishu"
	imMattermost "github.com/Tencent/WeKnora/internal/im/mattermost"
	imQQBot "github.com/Tencent/WeKnora/internal/im/qqbot"
	imSlack "github.com/Tencent/WeKnora/internal/im/slack"
	imTelegram "github.com/Tencent/WeKnora/internal/im/telegram"
	imWechat "github.com/Tencent/WeKnora/internal/im/wechat"
	imWecom "github.com/Tencent/WeKnora/internal/im/wecom"
	imYunzhijia "github.com/Tencent/WeKnora/internal/im/yunzhijia"
	"github.com/Tencent/WeKnora/internal/infrastructure/web_search"
)

// This file registers the implementations behind the builtin plugins with
// the domain registries, so every builtin capability is declared in this
// package: implementations here, descriptions in builtin.go.

// NewConnectorRegistry returns the data source registry holding every builtin
// connector.
func NewConnectorRegistry() (*datasource.ConnectorRegistry, error) {
	registry := datasource.NewConnectorRegistry()
	connectors := []datasource.Connector{
		wiki.NewConnector(core.RegionFeishu),
		// Lark is Feishu's international cloud: same connector, other host.
		wiki.NewConnector(core.RegionLark),
		// Drive (云盘) mode is its own type so the registry dispatches to the
		// Drive connector; it shares the Feishu client and export logic.
		drive.NewDriveConnector(core.RegionFeishuDrive),
		drive.NewDriveConnector(core.RegionLarkDrive),
		notionConnector.NewConnector(),
		confluenceConnector.NewConnector(),
		yuqueConnector.NewConnector(),
		dingtalkConnector.NewConnector(),
		imaConnector.NewConnector(),
		rssConnector.NewConnector(),
		gitlabConnector.NewConnector(),
	}
	var errs error
	for _, c := range connectors {
		if err := registry.Register(c); err != nil {
			errs = errors.Join(errs, fmt.Errorf("register %s connector: %w", c.Type(), err))
		}
	}
	if errs != nil {
		return nil, errs
	}
	return registry, nil
}

// RegisterWebSearchProviders registers every builtin web search provider.
func RegisterWebSearchProviders(registry *web_search.Registry) {
	for id, factory := range map[string]web_search.ProviderFactory{
		"duckduckgo": web_search.NewDuckDuckGoProvider,
		"google":     web_search.NewGoogleProvider,
		"bing":       web_search.NewBingProvider,
		"tavily":     web_search.NewTavilyProvider,
		"ollama":     web_search.NewOllamaProvider,
		"baidu":      web_search.NewBaiduProvider,
		"searxng":    web_search.NewSearxngProvider,
		"keenable":   web_search.NewKeenableProvider,
		"zhipu":      web_search.NewZhipuProvider,
		"exa":        web_search.NewExaProvider,
		"metaso":     web_search.NewMetasoProvider,
		"bocha":      web_search.NewBochaProvider,
		"brave":      web_search.NewBraveProvider,
		"serply":     web_search.NewSerplyProvider,
	} {
		registry.Register(id, factory)
	}
}

// RegisterIMAdapters registers every builtin IM platform adapter. Each
// platform's factory lives in its own subpackage.
func RegisterIMAdapters(s *im.Service) {
	s.RegisterAdapterFactory("wecom", imWecom.NewFactory())
	s.RegisterAdapterFactory("feishu", imFeishu.NewFactory(imFeishu.RegionFeishu))
	// Lark is Feishu's international cloud: same adapter, other host/tenant.
	s.RegisterAdapterFactory("lark", imFeishu.NewFactory(imFeishu.RegionLark))
	s.RegisterAdapterFactory("slack", imSlack.NewFactory())
	s.RegisterAdapterFactory("telegram", imTelegram.NewFactory())
	s.RegisterAdapterFactory("dingtalk", imDingtalk.NewFactory())
	s.RegisterAdapterFactory("mattermost", imMattermost.NewFactory())
	s.RegisterAdapterFactory("wechat", imWechat.NewFactory())
	s.RegisterAdapterFactory("qqbot", imQQBot.NewFactory())
	s.RegisterAdapterFactory("yunzhijia", imYunzhijia.NewFactory())
}
