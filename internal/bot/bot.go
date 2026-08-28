package bot

import (
	"log"

	"github.com/bwmarrin/discordgo"
	"gorm.io/gorm"

	"krew-toolie/internal/database/repository"
	"krew-toolie/internal/handlers"
)

const (
	commandAddTool    = "addtool"
	commandBorrow     = "borrow"
	commandReturn     = "return"
	commandRemoveTool = "removetool"
	commandMyTools    = "mytools"
	commandAvailable  = "available"
)

type Bot struct {
	session *discordgo.Session
	handler *handlers.CommandHandler
}

// NewBot creates the bot and wires up repositories, handlers, and slash
// command registration. It does not open any network connections yet.
func NewBot(token string, db *gorm.DB) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	userRepo := repository.NewUserRepository(db)
	toolRepo := repository.NewToolRepository(db)
	handler := handlers.NewCommandHandler(userRepo, toolRepo)

	b := &Bot{
		session: session,
		handler: handler,
	}

	// Handle slash command interactions and button clicks (components).
	session.AddHandler(handler.HandleInteraction)
	session.AddHandler(handler.HandleComponentInteraction)
	// Register slash commands once the gateway connection is ready, since the
	// application/user ID is only available after the session has connected.
	session.AddHandler(b.onReady)

	return b, nil
}

// Start opens the websocket connection to Discord and registers slash commands.
// It returns once the connection is established; the caller should keep the
// process alive and call Stop to shut down.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return err
	}

	log.Println("Bot is now running. Press CTRL-C to exit.")
	return nil
}

// Stop gracefully closes the Discord session.
func (b *Bot) Stop() {
	if err := b.session.Close(); err != nil {
		log.Printf("Error closing Discord session: %v", err)
	}
}

func (b *Bot) onReady(_ *discordgo.Session, r *discordgo.Ready) {
	log.Printf("Logged in as %s#%s", r.User.Username, r.User.Discriminator)

	commands := []*discordgo.ApplicationCommand{
		{
			Name:        commandAddTool,
			Description: "Add a tool to your collection",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Name of the tool",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "store_link",
					Description: "Optional store link",
					Required:    false,
				},
			},
		},
		{
			Name:        commandBorrow,
			Description: "Borrow a tool from a user, or list a user's tools",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "user_name",
					Description: "Username of the owner (fuzzy)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "tool_name",
					Description: "Name of the tool to borrow (fuzzy); omit to list the user's tools",
					Required:    false,
				},
			},
		},
		{
			Name:        commandRemoveTool,
			Description: "Remove one of your own tools",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "tool_name",
					Description: "Name of the tool to remove (fuzzy)",
					Required:    true,
				},
			},
		},
		{
			Name:        commandReturn,
			Description: "Return a borrowed tool",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "tool_name",
					Description: "Name of the tool to return",
					Required:    true,
				},
			},
		},
		{
			Name:        commandMyTools,
			Description: "List all tools you own",
		},
		{
			Name:        commandAvailable,
			Description: "List all available tools",
		},
	}

	for _, cmd := range commands {
		_, err := b.session.ApplicationCommandCreate(b.session.State.User.ID, "", cmd)
		if err != nil {
			log.Printf("Failed to create command %s: %v", cmd.Name, err)
			continue
		}
		log.Printf("Registered command: %s", cmd.Name)
	}
}
