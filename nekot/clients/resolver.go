package clients

import "github.com/BalanceBalls/nekot/util"

var registeredClient util.LlmClient

/** RegisterCustomClient registers an external LlmClient to be used instead of built-in providers. */
func RegisterCustomClient(client util.LlmClient) {
	registeredClient = client
}

func ResolveLlmClient(apiType string, apiUrl string, systemMessage string) util.LlmClient {
	if registeredClient != nil {
		return registeredClient
	}

	switch apiType {
	case util.OpenAiProviderType:
		return NewOpenAiClient(apiUrl, systemMessage)
	case util.GeminiProviderType:
		return NewGeminiClient(systemMessage)
	case util.OpenrouterProviderType:
		return NewOpenrouterClient(systemMessage)
	default:
		panic("Api type not supported: " + apiType)
	}
}
