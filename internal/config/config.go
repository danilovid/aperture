package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application configuration.
type Config struct {
	Port          int
	Env           string
	OpenAIBaseURL string
	DatabaseURL   string
	AdminAPIKey   string
}

const defaultOpenAIBaseURL = "https://api.openai.com"

// Load reads configuration from environment.
func Load() (*Config, error) {
	port := 8080
	if v := os.Getenv("PORT"); v != "" {
		var err error
		port, err = strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}

	return &Config{
		Port:          port,
		Env:           env,
		OpenAIBaseURL: baseURL,
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AdminAPIKey:   os.Getenv("ADMIN_API_KEY"),
	}, nil
}
