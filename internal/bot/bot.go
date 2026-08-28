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
	// guildID targets slash command registration to a specific server when set.
	// If empty, the bot registers commands globally (Discord caches these for up
	// to ~1 hour). Set it for instant command propagation.
	guildID string
}

// NewBot creates the bot and wires up repositories, handlers, and slash
// command registration. It does not open any network connections yet.
// guildID is optional: when non-empty, commands are registered at the guild
// (server) scope so they appear immediately.
func NewBot(token string, db *gorm.DB, guildID string) (*Bot, error) {
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
		guildID: guildID,
	}

	// Handle slash command interactions and button clicks (components).
	session.AddHandler(handler.HandleInteraction)
	session.AddHandler(handler.HandleComponentInteraction)
	// Register/clean up slash commands once the gateway connection is ready,
	// since the application/user ID is only available after the session connects.
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

	guildID := b.guildID
	if guildID == "" && len(r.Guilds) == 1 {
		// Auto-select the single guild the bot is in for instant propagation.
		guildID = r.Guilds[0].ID
		log.Printf("Auto-selected guild for command sync: %s", guildID)
	}

	b.syncCommands(guildID)
}

// botCommands returns the canonical set of slash commands.
func (b *Bot) botCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
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
}

// syncCommands deletes all of the bot's slash commands within the given scope
// and recreates the canonical set from botCommands(). Deleting everything first
// guarantees there are no stale, duplicate, or conflicting definitions left over
// from earlier deployments (such as old /removetool copies that could shadow the
// current one). When guildID is non-empty the commands are registered, and
// propagate immediately, at the guild (server) scope. When empty, commands are
// global, which Discord can cache per-guild for up to ~1 hour before showing.
func (b *Bot) syncCommands(guildID string) {
	appID := b.session.State.User.ID
	scope := ""
	if guildID != "" {
		scope = guildID
	}

	existing, err := b.session.ApplicationCommands(appID, scope)
	if err != nil {
		log.Printf("Failed to list existing commands: %v", err)
		return
	}

	// Remove every existing command in this scope so we start from a clean slate.
	for _, cmd := range existing {
		if delErr := b.session.ApplicationCommandDelete(appID, scope, cmd.ID); delErr != nil {
			log.Printf("Failed to delete command %s: %v", cmd.Name, delErr)
		} else {
			log.Printf("Removed command: %s", cmd.Name)
		}
	}

	// Recreate the canonical command set.
	for _, cmd := range b.botCommands() {
		if _, createErr := b.session.ApplicationCommandCreate(appID, scope, cmd); createErr != nil {
			log.Printf("Failed to create command %s: %v", cmd.Name, createErr)
			continue
		}
		log.Printf("Registered command: %s", cmd.Name)
	}
}
