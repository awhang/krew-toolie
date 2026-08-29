package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
)

// handleRemoveTool removes one of the caller's own tools (owner-scoped).
func (h *CommandHandler) handleRemoveTool(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	toolName := strings.TrimSpace(options["tool_name"])
	if toolName == "" {
		h.respondEphemeralError(s, i, "Tool name cannot be empty.")
		return
	}

	ctx := context.Background()
	own, err := h.toolRepo.GetByOwner(ctx, caller.ID)
	if err != nil {
		h.respondEphemeralError(s, i, "Could not fetch your tools.")
		return
	}

	matches := fuzzyMatchTools(own, toolName)
	if len(matches) == 0 {
		h.respondEphemeralError(s, i, fmt.Sprintf("No tool matching **%s** was found in your collection.", toolName))
		return
	}

	if len(matches) == 1 {
		tool := matches[0]
		h.respondConfirm(s, i,
			fmt.Sprintf("Remove **%s** from your collection?", tool.Name),
			actionRemove, tool.ID)
		return
	}

	h.respondSelectMenu(s, i,
		fmt.Sprintf("Multiple tools match **%s**. Choose one to remove:", toolName),
		actionRemove, matches)
}

func (h *CommandHandler) doRemove(s *discordgo.Session, i *discordgo.InteractionCreate, toolID string, caller *database.User) {
	ctx := context.Background()
	if err := h.toolRepo.Remove(ctx, toolID, caller.ID); err != nil {
		h.respondEphemeralError(s, i, err.Error())
		return
	}

	h.respond(s, i, "Tool removed from your collection.")
}
