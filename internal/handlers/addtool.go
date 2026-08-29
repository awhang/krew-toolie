package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
)

// handleAddTool adds a tool to the caller's collection.
func (h *CommandHandler) handleAddTool(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	name := strings.TrimSpace(options["name"])
	if name == "" {
		h.respondEphemeralError(s, i, "Tool name cannot be empty.")
		return
	}

	var storeLink *string
	if link := strings.TrimSpace(options["store_link"]); link != "" {
		storeLink = &link
	}

	exists, err := h.toolRepo.ExistsByName(context.Background(), name, caller.ID)
	if err != nil {
		h.respondEphemeralError(s, i, "Could not check your tools.")
		return
	}
	if exists {
		h.respondEphemeralError(s, i, fmt.Sprintf("You already have a tool named **%s**.", name))
		return
	}

	tool := &database.Tool{
		Name:      name,
		StoreLink: storeLink,
		OwnerID:   caller.ID,
	}
	if err := h.toolRepo.Create(context.Background(), tool); err != nil {
		h.respondEphemeralError(s, i, "Failed to add tool.")
		return
	}

	h.respond(s, i, fmt.Sprintf("Tool **%s** added to your collection!", name))
}
