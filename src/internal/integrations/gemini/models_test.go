package gemini

import (
	"testing"
)

func TestLLMStageConfigValidation(t *testing.T) {
	// Valid
	valid := LLMStageConfig{
		Model:         ModelGemini25Flash,
		ThinkingLevel: ThinkingHigh,
	}
	if err := ValidateStageConfig(valid); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}

	// Invalid model
	invalidModel := LLMStageConfig{
		Model:         "gpt-4o",
		ThinkingLevel: ThinkingLow,
	}
	if err := ValidateStageConfig(invalidModel); err == nil {
		t.Fatalf("expected error for unsupported model")
	}

	// Invalid thinking level
	invalidTL := LLMStageConfig{
		Model:         ModelGemini25FlashLite,
		ThinkingLevel: ThinkingLevel("ultra_deep"),
	}
	if err := ValidateStageConfig(invalidTL); err == nil {
		t.Fatalf("expected error for unsupported thinking level")
	}
}

func TestResolveStageConfig_Precedence(t *testing.T) {
	systemDefault := LLMStageConfig{
		Model:         ModelGemini25FlashLite,
		ThinkingLevel: ThinkingLow,
	}

	userPrefs := UserLLMPreferences{
		Polish: LLMStageConfig{
			Model:         ModelGemini25Flash,
			ThinkingLevel: ThinkingMedium,
		},
	}

	jobOverride := LLMStageConfig{
		Model:         ModelGemini25Pro,
		ThinkingLevel: ThinkingHigh,
	}

	// 1. Job override wins
	resolved := ResolveStageConfig("polish", &jobOverride, &userPrefs, systemDefault)
	if resolved.Model != ModelGemini25Pro || resolved.ThinkingLevel != ThinkingHigh {
		t.Fatalf("expected jobOverride, got %+v", resolved)
	}

	// 2. User prefs wins when no job override
	resolved = ResolveStageConfig("polish", nil, &userPrefs, systemDefault)
	if resolved.Model != ModelGemini25Flash || resolved.ThinkingLevel != ThinkingMedium {
		t.Fatalf("expected userPrefs, got %+v", resolved)
	}

	// 3. System default wins when user prefs has empty config
	resolved = ResolveStageConfig("structure", nil, &UserLLMPreferences{}, systemDefault)
	if resolved.Model != ModelGemini25FlashLite || resolved.ThinkingLevel != ThinkingLow {
		t.Fatalf("expected systemDefault, got %+v", resolved)
	}
}

func TestBuildThinkingConfig(t *testing.T) {
	noneCfg := BuildThinkingConfig(ThinkingNone)
	if noneCfg.ThinkingBudget == nil || *noneCfg.ThinkingBudget != 0 {
		t.Fatalf("expected 0 budget for none, got %v", noneCfg.ThinkingBudget)
	}

	highCfg := BuildThinkingConfig(ThinkingHigh)
	if highCfg.ThinkingBudget == nil || *highCfg.ThinkingBudget != 16384 {
		t.Fatalf("expected 16384 budget for high, got %v", highCfg.ThinkingBudget)
	}
}

