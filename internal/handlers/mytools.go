package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
)

// handleMyTools lists the tools the caller owns.
func (h *CommandHandler) handleMyTools(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	tools, err := h.toolRepo.GetByOwner(context.Background(), caller.ID)
	if err != nil {
		h.respondEphemeralError(s, i, "Could not fetch your tools.")
		return
	}

	if len(tools) == 0 {
		h.respondEphemeral(s, i, "You don't own any tools yet. Use `/addtool` to add one.")
		return
	}

	var b strings.Builder
	b.WriteString("**Your tools:**\n")
	for _, t := range tools {
		b.WriteString(fmt.Sprintf("- %s — %s\n", t.Name, statusText(t)))
	}

	h.respondEphemeral(s, i, b.String())
}
