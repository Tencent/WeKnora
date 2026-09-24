package providers

import "github.com/Tencent/WeKnora/internal/models/internal/configcopy"

// Builtins returns independent provider definitions without registering global state.
func Builtins() []*Definition {
	return configcopy.Clone([]*Definition{
		newAliyunProvider(),
		newAnthropicProvider(),
		newAzureOpenaiProvider(),
		newDeepseekProvider(),
		newGeminiProvider(),
		newGenericProvider(),
		newGpustackProvider(),
		newHunyuanProvider(),
		newJinaProvider(),
		newLitellmProvider(),
		newLkeapProvider(),
		newLongcatProvider(),
		newMimoProvider(),
		newMinimaxProvider(),
		newModelscopeProvider(),
		newMoonshotProvider(),
		newNovitaProvider(),
		newNvidiaProvider(),
		newOpenaiProvider(),
		newOpenrouterProvider(),
		newQianfanProvider(),
		newQiniuProvider(),
		newRequestyProvider(),
		newSiliconflowProvider(),
		newVolcengineProvider(),
		newWeKnoraCloudProvider(),
		newZhipuProvider(),
	})
}
