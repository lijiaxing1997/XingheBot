package llm

import (
	"fmt"
	"strings"
)

type ModelType string

const (
	ModelTypeOpenAI      ModelType = "openai"
	ModelTypeAnthropics  ModelType = "anthropics"
	modelTypeAnthropicV1 ModelType = "anthropic"
)

func ParseModelType(raw string) (ModelType, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", string(ModelTypeOpenAI):
		return ModelTypeOpenAI, nil
	case string(ModelTypeAnthropics), string(modelTypeAnthropicV1):
		return ModelTypeAnthropics, nil
	default:
		return "", fmt.Errorf("unsupported model_config.model_type %q (supported: %q, %q)", raw, ModelTypeOpenAI, ModelTypeAnthropics)
	}
}
