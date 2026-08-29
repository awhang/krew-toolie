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
	commandToolieHelp = "tooliehelp"
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

	// Handle slash command interactions, select menus, and button clicks.
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

	// Guild commands only: always resolve a guild to register into. If
	// GUILD_ID is set, it is used directly for instant propagation. Otherwise,
	// if the bot is in exactly one server, that server is auto-selected. If no
	// unique guild can be determined, we skip registration rather than falling
	// back to slow-propagating global commands.
	guildID := b.guildID
	if guildID == "" {
		switch len(r.Guilds) {
		case 1:
			guildID = r.Guilds[0].ID
			log.Printf("Auto-selected guild for command sync: %s", guildID)
		case 0:
			log.Printf("WARNING: no guilds available; skipping command registration (guild-only mode).")
			return
		default:
			log.Printf("WARNING: bot is in %d guilds and GUILD_ID is not set; skipping command registration (guild-only mode). Set GUILD_ID to target a server.", len(r.Guilds))
			return
		}
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
			Description: "Borrow a tool by name (fuzzy), optionally narrowed to an owner",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "tool_name",
					Description: "Name of the tool to borrow (fuzzy)",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "owner",
					Description: "Optional owner to borrow from (name/nickname, fuzzy)",
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
					Description: "Name of the tool to return (fuzzy)",
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
			Description: "List tools you can borrow, grouped by owner",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "tool",
					Description: "Optional tool name filter (fuzzy)",
					Required:    false,
				},
			},
		},
		{
			Name:        commandToolieHelp,
			Description: "Quick reference for how to use the Toolie bot",
		},
	}
}

// syncCommands registers and refreshes the bot's slash commands.
//
// The bot runs in guild-only mode: it registers commands only at the given
// guild scope (so updates propagate immediately). It also purges any leftover
// global commands from earlier deployments, because Discord does not expire
// them on its own and leftover global definitions with the same names can
// shadow or confuse the guild-scoped commands registered to a server.
func (b *Bot) syncCommands(guildID string) {
	appID := b.session.State.User.ID

	if guildID == "" {
		// Guild-only mode: never register global commands.
		log.Printf("WARNING: refusing to register global commands (guild-only mode). Set GUILD_ID or let the bot auto-detect its single server.")
		return
	}

	// Purge leftover global commands first so they can't conflict with the
	// guild-scoped commands.
	b.clearCommands(appID, "")
	// Then rebuild the guild-scoped command set (delete + recreate).
	b.clearCommands(appID, guildID)
	b.recreateCommands(appID, guildID)
}

// clearCommands deletes every registered command at the given scope. An empty
// scope refers to the bot's global commands.
func (b *Bot) clearCommands(appID, scope string) {
	existing, err := b.session.ApplicationCommands(appID, scope)
	if err != nil {
		log.Printf("Failed to list commands (scope=%q): %v", scope, err)
		return
	}
	for _, cmd := range existing {
		if delErr := b.session.ApplicationCommandDelete(appID, scope, cmd.ID); delErr != nil {
			log.Printf("Failed to delete command %s (scope=%q): %v", cmd.Name, scope, delErr)
		} else {
			log.Printf("Removed command %s (scope=%q)", cmd.Name, scope)
		}
	}
}

// recreateCommands registers the canonical command set at the given scope. An
// empty scope refers to the bot's global commands.
func (b *Bot) recreateCommands(appID, scope string) {
	for _, cmd := range b.botCommands() {
		if _, createErr := b.session.ApplicationCommandCreate(appID, scope, cmd); createErr != nil {
			log.Printf("Failed to create command %s (scope=%q): %v", cmd.Name, scope, createErr)
			continue
		}
		log.Printf("Registered command %s (scope=%q)", cmd.Name, scope)
	}
}
