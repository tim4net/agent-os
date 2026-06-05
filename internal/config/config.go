package config

import "os"

// Config holds application configuration loaded from environment variables.
type Config struct {
	DatabaseURL      string
	LiteLLMURL       string
	ObsidianPath     string
	ArtifactsPath    string
	HermesSkillsPath string
	AnthropicAPIKey  string
	OpenAIAPIKey     string
	XAIAPIKey        string
	OpenRouterAPIKey string
	GeminiAPIKey     string
	FALKey           string
	ZAIAPIKey        string
	HermesAPIKey     string
	LLMModel         string
	Port             string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		DatabaseURL:      getEnv("DATABASE_URL", "postgres://localhost:5432/agentos?sslmode=disable"),
		LiteLLMURL:       getEnv("LITELLM_URL", "http://localhost:4000"),
		ObsidianPath:     getEnv("OBSIDIAN_PATH", "./obsidian"),
		ArtifactsPath:    getEnv("ARTIFACTS_PATH", "/data/artifacts"),
		HermesSkillsPath: getEnv("HERMES_SKILLS_PATH", "/data/hermes-skills"),
		AnthropicAPIKey:  getEnv("ANTHROPIC_API_KEY", ""),
		OpenAIAPIKey:     getEnv("OPENAI_API_KEY", ""),
		XAIAPIKey:        getEnv("XAI_API_KEY", ""),
		OpenRouterAPIKey: getEnv("OPENROUTER_API_KEY", ""),
		GeminiAPIKey:     getEnv("GEMINI_API_KEY", getEnv("GOOGLE_API_KEY", "")),
		FALKey:           getEnv("FAL_KEY", ""),
		ZAIAPIKey:        getEnv("ZAI_API_KEY", ""),
		HermesAPIKey:     getEnv("HERMES_API_KEY", ""),
		LLMModel:         getEnv("LLM_MODEL", "local-qwen"),
		Port:             getEnv("PORT", "8080"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
