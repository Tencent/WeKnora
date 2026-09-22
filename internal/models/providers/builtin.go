package providers

import (
	"sync"

	"github.com/Tencent/WeKnora/internal/models/catalog"
)

var builtinOnce sync.Once

// EnsureBuiltins initializes the shared registry once, including for standalone clients.
func EnsureBuiltins() { builtinOnce.Do(registerBuiltins) }

// registerBuiltins installs fresh built-in descriptions. Call at composition time.
func registerBuiltins() {
	catalog.Register(newAliyunProvider())
	catalog.Register(newAnthropicProvider())
	catalog.Register(newAzureOpenaiProvider())
	catalog.Register(newDeepseekProvider())
	catalog.Register(newGeminiProvider())
	catalog.Register(newGenericProvider())
	catalog.Register(newGpustackProvider())
	catalog.Register(newHunyuanProvider())
	catalog.Register(newJinaProvider())
	catalog.Register(newLitellmProvider())
	catalog.Register(newLkeapProvider())
	catalog.Register(newLongcatProvider())
	catalog.Register(newMimoProvider())
	catalog.Register(newMinimaxProvider())
	catalog.Register(newModelscopeProvider())
	catalog.Register(newMoonshotProvider())
	catalog.Register(newNovitaProvider())
	catalog.Register(newNvidiaProvider())
	catalog.Register(newOpenaiProvider())
	catalog.Register(newOpenrouterProvider())
	catalog.Register(newQianfanProvider())
	catalog.Register(newQiniuProvider())
	catalog.Register(newRequestyProvider())
	catalog.Register(newSiliconflowProvider())
	catalog.Register(newVolcengineProvider())
	catalog.Register(newWeKnoraCloudProvider())
	catalog.Register(newZhipuProvider())
}
