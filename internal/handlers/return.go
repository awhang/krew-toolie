package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
)

// handleReturn returns a tool the caller is currently borrowing.
func (h *CommandHandler) handleReturn(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	toolName := strings.TrimSpace(options["tool_name"])
	if toolName == "" {
		h.respondEphemeralError(s, i, "Tool name is required.")
		return
	}

	ctx := context.Background()
	borrowed, err := h.toolRepo.GetBorrowedBy(ctx, caller.ID)
	if err != nil {
		h.respondEphemeralError(s, i, "Could not fetch borrowed tools.")
		return
	}

	matches := fuzzyMatchTools(borrowed, toolName)
	if len(matches) == 0 {
		h.respondEphemeralError(s, i, fmt.Sprintf("No tool matching **%s** is currently borrowed by you.", toolName))
		return
	}

	if len(matches) == 1 {
		tool := matches[0]
		h.respondConfirm(s, i,
			fmt.Sprintf("Return **%s**?", tool.Name),
			actionReturn, tool.ID)
		return
	}

	h.respondSelectMenu(s, i,
		fmt.Sprintf("Multiple borrowed tools match **%s**. Choose one:", toolName),
		actionReturn, matches)
}

func (h *CommandHandler) doReturn(s *discordgo.Session, i *discordgo.InteractionCreate, toolID string, caller *database.User) {
	ctx := context.Background()
	if err := h.toolRepo.Return(ctx, toolID, caller.ID); err != nil {
		h.respondEphemeralError(s, i, err.Error())
		return
	}

	h.respond(s, i, "Tool returned.")
}
