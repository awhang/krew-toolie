package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
	"krew-toolie/internal/database/repository"
)

type CommandHandler struct {
	userRepo *repository.UserRepository
	toolRepo *repository.ToolRepository
}

func NewCommandHandler(userRepo *repository.UserRepository, toolRepo *repository.ToolRepository) *CommandHandler {
	return &CommandHandler{
		userRepo: userRepo,
		toolRepo: toolRepo,
	}
}

func (h *CommandHandler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	ctx := context.Background()

	// Ensure user exists in DB.
	user, err := h.userRepo.GetOrCreate(ctx, i.Member.User.ID, i.Member.User.Username)
	if err != nil {
		h.respondError(s, i, "Database error. Please try again later.")
		return
	}

	switch data.Name {
	case "addtool":
		h.handleAddTool(s, i, ctx, user)
	case "borrow":
		h.handleBorrow(s, i, ctx, user)
	case "return":
		h.handleReturn(s, i, ctx, user)
	case "mytools":
		h.handleMyTools(s, i, ctx, user)
	case "available":
		h.handleAvailable(s, i, ctx)
	}
}

func (h *CommandHandler) handleAddTool(s *discordgo.Session, i *discordgo.InteractionCreate, ctx context.Context, user *database.User) {
	options := i.ApplicationCommandData().Options
	name := strings.TrimSpace(options[0].StringValue())
	if name == "" {
		h.respondError(s, i, "Tool name cannot be empty.")
		return
	}

	var storeLink *string
	if len(options) > 1 {
		link := strings.TrimSpace(options[1].StringValue())
		if link != "" {
			storeLink = &link
		}
	}

	tool := &database.Tool{
		Name:      name,
		StoreLink: storeLink,
		OwnerID:   user.ID,
	}

	if err := h.toolRepo.Create(ctx, tool); err != nil {
		h.respondError(s, i, "Failed to add tool.")
		return
	}

	h.respond(s, i, fmt.Sprintf("Tool **%s** added to your collection!", name))
}

func (h *CommandHandler) handleBorrow(s *discordgo.Session, i *discordgo.InteractionCreate, ctx context.Context, user *database.User) {
	toolName := strings.TrimSpace(i.ApplicationCommandData().Options[0].StringValue())

	if err := h.toolRepo.BorrowByName(ctx, toolName, user.ID); err != nil {
		h.respondError(s, i, err.Error())
		return
	}

	h.respond(s, i, fmt.Sprintf("You've borrowed **%s**!", toolName))
}

func (h *CommandHandler) handleReturn(s *discordgo.Session, i *discordgo.InteractionCreate, ctx context.Context, user *database.User) {
	toolName := strings.TrimSpace(i.ApplicationCommandData().Options[0].StringValue())

	if err := h.toolRepo.ReturnByName(ctx, toolName); err != nil {
		h.respondError(s, i, err.Error())
		return
	}

	h.respond(s, i, fmt.Sprintf("**%s** has been returned!", toolName))
}

func (h *CommandHandler) handleMyTools(s *discordgo.Session, i *discordgo.InteractionCreate, ctx context.Context, user *database.User) {
	tools, err := h.toolRepo.GetByOwner(ctx, user.ID)
	if err != nil {
		h.respondError(s, i, "Could not fetch your tools.")
		return
	}

	if len(tools) == 0 {
		h.respond(s, i, "You don't own any tools yet. Use `/addtool` to add one.")
		return
	}

	var b strings.Builder
	b.WriteString("**Your tools:**\n")
	for _, t := range tools {
		status := ":white_check_mark: available"
		if t.BorrowerID != nil && *t.BorrowerID != "" {
			status = ":lock: borrowed"
		}
		b.WriteString(fmt.Sprintf("- %s — %s\n", t.Name, status))
	}

	h.respond(s, i, b.String())
}

func (h *CommandHandler) handleAvailable(s *discordgo.Session, i *discordgo.InteractionCreate, ctx context.Context) {
	tools, err := h.toolRepo.GetAvailable(ctx)
	if err != nil {
		h.respondError(s, i, "Could not fetch available tools.")
		return
	}

	if len(tools) == 0 {
		h.respond(s, i, "No tools are currently available.")
		return
	}

	var b strings.Builder
	b.WriteString("**Available tools:**\n")
	for _, t := range tools {
		b.WriteString(fmt.Sprintf("- %s (owned by %s)\n", t.Name, t.Owner.Username))
	}

	h.respond(s, i, b.String())
}

func (h *CommandHandler) respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
		},
	})
}

func (h *CommandHandler) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	h.respond(s, i, "❌ "+message)
}
