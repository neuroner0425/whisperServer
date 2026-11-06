package gemini

import (
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// ThinkingLevel specifies the reasoning effort level allocated to Gemini.
type ThinkingLevel string

const (
	ThinkingNone   ThinkingLevel = "none"   // thinkingBudget: 0
	ThinkingLow    ThinkingLevel = "low"    // thinkingBudget: 1024
	ThinkingMedium ThinkingLevel = "medium" // thinkingBudget: 4096
	ThinkingHigh   ThinkingLevel = "high"   // thinkingBudget: 16384
)

// Supported canonical Gemini model identifiers.
const (
	ModelGemini25Flash     = "gemini-2.5-flash"
	ModelGemini25FlashLite = "gemini-2.5-flash-lite"
	ModelGemini25Pro       = "gemini-2.5-pro"
)

// SupportedModels defines the allowlist of valid models.
var SupportedModels = map[string]string{
	ModelGemini25Flash:     "Gemini 2.5 Flash (균형 잡힌 고성능 모델)",
	ModelGemini25FlashLite: "Gemini 2.5 Flash-Lite (빠르고 경제적인 경량 모델)",
	ModelGemini25Pro:       "Gemini 2.5 Pro (복잡한 추론 및 최고 정확도 모델)",
}

// SupportedThinkingLevels defines the allowlist of valid thinking levels.
var SupportedThinkingLevels = map[ThinkingLevel]int32{
	ThinkingNone:   0,
	ThinkingLow:    1024,
	ThinkingMedium: 4096,
	ThinkingHigh:   16384,
}

// LLMStageConfig defines model and thinking depth for a specific workflow stage.
type LLMStageConfig struct {
	Model         string        `json:"model"`
	ThinkingLevel ThinkingLevel `json:"thinking_level"`
}

// UserLLMPreferences contains stage-specific preferences per user.
type UserLLMPreferences struct {
	Polish      LLMStageConfig `json:"polish"`
	Structure   LLMStageConfig `json:"structure"`
	PDFExtract  LLMStageConfig `json:"pdf_extract"`
	Derivatives LLMStageConfig `json:"derivatives"`
}

// DefaultPreferences returns the default recommended configurations across all stages.
func DefaultPreferences() UserLLMPreferences {
	return UserLLMPreferences{
		Polish: LLMStageConfig{
			Model:         ModelGemini25FlashLite,
			ThinkingLevel: ThinkingLow,
		},
		Structure: LLMStageConfig{
			Model:         ModelGemini25Flash,
			ThinkingLevel: ThinkingMedium,
		},
		PDFExtract: LLMStageConfig{
			Model:         ModelGemini25Flash,
			ThinkingLevel: ThinkingHigh,
		},
		Derivatives: LLMStageConfig{
			Model:         ModelGemini25Flash,
			ThinkingLevel: ThinkingMedium,
		},
	}
}

// ValidateStageConfig ensures model and thinking level adhere to the server allowlist.
func ValidateStageConfig(cfg LLMStageConfig) error {
	model := strings.TrimSpace(cfg.Model)
	if _, ok := SupportedModels[model]; !ok {
		return fmt.Errorf("unsupported model %q: allowed models are gemini-2.5-flash, gemini-2.5-flash-lite, gemini-2.5-pro", cfg.Model)
	}

	tl := ThinkingLevel(strings.ToLower(strings.TrimSpace(string(cfg.ThinkingLevel))))
	if _, ok := SupportedThinkingLevels[tl]; !ok {
		return fmt.Errorf("unsupported thinking_level %q: allowed levels are none, low, medium, high", cfg.ThinkingLevel)
	}
	return nil
}

// ValidateUserPreferences validates all stage configurations in the user preference payload.
func ValidateUserPreferences(prefs UserLLMPreferences) error {
	if err := ValidateStageConfig(prefs.Polish); err != nil {
		return fmt.Errorf("invalid polish config: %w", err)
	}
	if err := ValidateStageConfig(prefs.Structure); err != nil {
		return fmt.Errorf("invalid structure config: %w", err)
	}
	if err := ValidateStageConfig(prefs.PDFExtract); err != nil {
		return fmt.Errorf("invalid pdf_extract config: %w", err)
	}
	if err := ValidateStageConfig(prefs.Derivatives); err != nil {
		return fmt.Errorf("invalid derivatives config: %w", err)
	}
	return nil
}

// ResolveStageConfig determines the effective LLM configuration using strict precedence:
// Job Override > User Preferences > System Default.
func ResolveStageConfig(stage string, jobOverride *LLMStageConfig, userPrefs *UserLLMPreferences, systemDefault LLMStageConfig) LLMStageConfig {
	// 1. Try job-level override
	if jobOverride != nil && jobOverride.Model != "" {
		if err := ValidateStageConfig(*jobOverride); err == nil {
			return *jobOverride
		}
	}

	// 2. Try user-level preference
	if userPrefs != nil {
		var candidate LLMStageConfig
		switch stage {
		case "polish":
			candidate = userPrefs.Polish
		case "structure":
			candidate = userPrefs.Structure
		case "pdf_extract":
			candidate = userPrefs.PDFExtract
		case "derivatives":
			candidate = userPrefs.Derivatives
		}
		if candidate.Model != "" && ValidateStageConfig(candidate) == nil {
			return candidate
		}
	}

	// 3. Fallback to system default
	if systemDefault.Model != "" && ValidateStageConfig(systemDefault) == nil {
		return systemDefault
	}

	// 4. Absolute fallback
	defaults := DefaultPreferences()
	switch stage {
	case "polish":
		return defaults.Polish
	case "structure":
		return defaults.Structure
	case "pdf_extract":
		return defaults.PDFExtract
	default:
		return defaults.Derivatives
	}
}

// BuildThinkingConfig maps the stage ThinkingLevel into genai.ThinkingConfig.
func BuildThinkingConfig(level ThinkingLevel) *genai.ThinkingConfig {
	budget, ok := SupportedThinkingLevels[level]
	if !ok || budget <= 0 {
		return &genai.ThinkingConfig{
			ThinkingBudget: genai.Ptr[int32](0),
		}
	}

	cfg := &genai.ThinkingConfig{
		ThinkingBudget: genai.Ptr[int32](budget),
	}
	switch level {
	case ThinkingLow:
		cfg.ThinkingLevel = genai.ThinkingLevelLow
	case ThinkingMedium:
		cfg.ThinkingLevel = genai.ThinkingLevelMedium
	case ThinkingHigh:
		cfg.ThinkingLevel = genai.ThinkingLevelHigh
	}
	return cfg
}

// CatalogResponse returns the API catalog for client settings screens.
type CatalogResponse struct {
	Models         map[string]string `json:"models"`
	ThinkingLevels []string          `json:"thinking_levels"`
	Current        UserLLMPreferences `json:"current"`
}
