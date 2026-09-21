// Package vendors links every built-in vendor package so their init
// functions register with the catalog. Import it for side effects from the
// binary's entry point (and from tests that need the full catalog).
package vendors

import (
	// Each vendor registers itself with the catalog in init.
	_ "github.com/Tencent/WeKnora/internal/models/vendors/aliyun"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/anthropic"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/azure_openai"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/deepseek"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/gemini"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/generic"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/gpustack"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/hunyuan"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/jina"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/litellm"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/lkeap"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/longcat"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/mimo"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/minimax"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/modelscope"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/moonshot"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/novita"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/nvidia"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/openai"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/openrouter"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/qianfan"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/qiniu"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/requesty"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/siliconflow"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/volcengine"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/weknoracloud"
	_ "github.com/Tencent/WeKnora/internal/models/vendors/zhipu"
)
