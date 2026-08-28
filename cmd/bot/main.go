package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"krew-toolie/internal/bot"
	"krew-toolie/internal/config"
	"krew-toolie/internal/database"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := database.NewPostgresDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	b, err := bot.NewBot(cfg.DiscordToken, db, cfg.GuildID)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := b.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	// Wait for an interrupt or termination signal, then shut down gracefully.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down...")
	b.Stop()
}
