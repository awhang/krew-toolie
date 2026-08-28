package config

import (
	"errors"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DiscordToken string
	DatabaseURL  string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	discordToken := os.Getenv("DISCORD_TOKEN")
	if discordToken == "" {
		return nil, errors.New("DISCORD_TOKEN is required")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://postgres:password@localhost:5432/bot_db?sslmode=disable"
	}

	return &Config{
		DiscordToken: discordToken,
		DatabaseURL:  databaseURL,
	}, nil
}
