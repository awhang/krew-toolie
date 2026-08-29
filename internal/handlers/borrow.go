package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
)

// handleBorrow borrows a tool by fuzzy name, optionally narrowed to an owner.
func (h *CommandHandler) handleBorrow(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	toolName := strings.TrimSpace(options["tool_name"])
	ownerQ := strings.TrimSpace(options["owner"])

	if toolName == "" {
		h.respondEphemeralError(s, i, "Tool name is required.")
		return
	}

	ctx := context.Background()

	var candidates []database.Tool
	ownerHint := ""
	if ownerQ != "" {
		owners, err := h.userRepo.FindByOwnerFuzzy(ctx, ownerQ)
		if err != nil {
			h.respondEphemeralError(s, i, "Could not search for owners.")
			return
		}
		if len(owners) == 0 {
			h.respondEphemeralError(s, i, fmt.Sprintf("No owner found matching **%s**.", ownerQ))
			return
		}
		if len(owners) > 1 {
			h.respondOwnersAmbiguous(s, i, owners)
			return
		}
		ownerHint = displayNameOf(owners[0])
		ownTools, err := h.toolRepo.GetByOwner(ctx, owners[0].ID)
		if err != nil {
			h.respondEphemeralError(s, i, "Could not fetch tools for that owner.")
			return
		}
		candidates = fuzzyMatchTools(ownTools, toolName)
	} else {
		allTools, err := h.toolRepo.List(ctx)
		if err != nil {
			h.respondEphemeralError(s, i, "Could not search for tools.")
			return
		}
		candidates = fuzzyMatchTools(allTools, toolName)
	}

	h.offerBorrow(s, i, caller, candidates, toolName, ownerHint)
}

func (h *CommandHandler) offerBorrow(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User, matches []database.Tool, toolName, ownerHint string) {
	if len(matches) == 0 {
		msg := fmt.Sprintf("No tool matching **%s** was found.", toolName)
		if ownerHint != "" {
			msg = fmt.Sprintf("No tool matching **%s** was found for **%s**.", toolName, ownerHint)
		}
		h.respondEphemeralError(s, i, msg)
		return
	}

	available := availableTools(matches)
	if len(available) == 0 {
		h.respondEphemeralError(s, i, "That tool is already borrowed.")
		return
	}

	if len(available) == 1 {
		tool := available[0]
		h.respondConfirm(s, i,
			fmt.Sprintf("Borrow **%s** (owned by %s)?", tool.Name, ownerLabel(tool)),
			actionBorrow, tool.ID)
		return
	}

	h.respondSelectMenu(s, i,
		fmt.Sprintf("Multiple tools match **%s**. Choose one:", toolName),
		actionBorrow, available)
}

func (h *CommandHandler) doBorrow(s *discordgo.Session, i *discordgo.InteractionCreate, toolID string, caller *database.User) {
	ctx := context.Background()
	tool, err := h.toolRepo.GetByID(ctx, toolID)
	if err != nil {
		h.respondEphemeralError(s, i, "Could not find that tool.")
		return
	}
	if tool == nil {
		h.respondEphemeralError(s, i, "That tool no longer exists.")
		return
	}

	if err := h.toolRepo.Borrow(ctx, toolID, caller.ID); err != nil {
		h.respondEphemeralError(s, i, err.Error())
		return
	}

	h.respond(s, i, fmt.Sprintf("You've borrowed **%s** from %s!", tool.Name, ownerLabel(*tool)))
}
