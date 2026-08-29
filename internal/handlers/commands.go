package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
	"krew-toolie/internal/database/repository"
	"krew-toolie/internal/fuzzy"
)

// Actions performed via select menus / buttons. Values encode
// "<action>:<toolID>"; "<action>:cancel" means the user cancelled.
const (
	actionBorrow = "borrow"
	actionRemove = "remove"
	actionReturn = "return"

	cancelToken = "cancel"
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

// HandleInteraction routes slash command interactions to their handlers.
func (h *CommandHandler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	callerID, username, globalName, serverName := interactionUserNames(i)
	caller, err := h.userRepo.GetOrCreate(context.Background(), callerID, username, globalName, serverName)
	if err != nil {
		h.respondEphemeralError(s, i, "Database error. Please try again later.")
		return
	}

	switch data.Name {
	case "addtool":
		h.handleAddTool(s, i, caller)
	case "borrow":
		h.handleBorrow(s, i, caller)
	case "return":
		h.handleReturn(s, i, caller)
	case "removetool":
		h.handleRemoveTool(s, i, caller)
	case "mytools":
		h.handleMyTools(s, i, caller)
	case "available":
		h.handleAvailable(s, i)
	}
}

// HandleComponentInteraction processes button clicks and select-menu choices
// for borrow/remove/return confirmation and disambiguation.
func (h *CommandHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	action, toolOrCancel, ok := resolveComponent(i.MessageComponentData())
	if !ok {
		return
	}

	if toolOrCancel == cancelToken {
		// User cancelled: acknowledge, change nothing, and remove the prompt.
		h.respondEphemeral(s, i, "Cancelled.")
		h.deletePrompt(s, i)
		return
	}

	callerID, username, globalName, serverName := interactionUserNames(i)
	caller, err := h.userRepo.GetOrCreate(context.Background(), callerID, username, globalName, serverName)
	if err != nil {
		h.respondEphemeralError(s, i, "Database error. Please try again later.")
		h.deletePrompt(s, i)
		return
	}

	switch action {
	case actionBorrow:
		h.doBorrow(s, i, toolOrCancel, caller)
	case actionRemove:
		h.doRemove(s, i, toolOrCancel, caller)
	case actionReturn:
		h.doReturn(s, i, toolOrCancel, caller)
	}
	// Remove the message that held the button/select menu now that the user
	// has made their choice.
	h.deletePrompt(s, i)
}

// deletePrompt removes the original message that contained the button(s) or
// select menu after the user has made their choice, since it is no longer
// needed. The prompts are ephemeral, so this deletes the original interaction
// response.
func (h *CommandHandler) deletePrompt(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := s.InteractionResponseDelete(i.Interaction); err != nil {
		log.Printf("Failed to delete prompt message: %v", err)
	}
}

// ---- /addtool ----

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

// ---- /borrow ----

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

// ---- /return ----

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

// ---- /removetool ----

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

// ---- /mytools & /available ----

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

func (h *CommandHandler) handleAvailable(s *discordgo.Session, i *discordgo.InteractionCreate) {
	tools, err := h.toolRepo.GetAvailable(context.Background())
	if err != nil {
		h.respondEphemeralError(s, i, "Could not fetch available tools.")
		return
	}

	if len(tools) == 0 {
		h.respondEphemeral(s, i, "No tools are currently available.")
		return
	}

	var b strings.Builder
	b.WriteString("**Available tools:**\n")
	for _, t := range tools {
		b.WriteString(fmt.Sprintf("- %s (owned by %s)\n", t.Name, ownerLabel(t)))
	}

	h.respondEphemeral(s, i, b.String())
}

func (h *CommandHandler) respondOwnersAmbiguous(s *discordgo.Session, i *discordgo.InteractionCreate, users []database.User) {
	var names []string
	for _, u := range users {
		names = append(names, displayNameOf(u))
	}
	h.respondEphemeralError(s, i, fmt.Sprintf("Found multiple owners matching: %s. Please be more specific.", strings.Join(names, ", ")))
}

// ---- response helpers ----

// respond sends a public message to the channel (successes only).
func (h *CommandHandler) respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}

func (h *CommandHandler) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *CommandHandler) respondEphemeralError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	h.respondEphemeral(s, i, "❌ "+message)
}

// respondConfirm shows an ephemeral prompt with Confirm and Cancel buttons.
func (h *CommandHandler) respondConfirm(s *discordgo.Session, i *discordgo.InteractionCreate, prompt, action, toolID string) {
	row := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Confirm", Style: discordgo.PrimaryButton, CustomID: componentID(action, toolID)},
		discordgo.Button{Label: "Cancel", Style: discordgo.SecondaryButton, CustomID: componentID(action, cancelToken)},
	}}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    prompt,
			Components: []discordgo.MessageComponent{row},
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

// respondSelectMenu shows an ephemeral select menu of matching tools plus a
// Cancel option.
func (h *CommandHandler) respondSelectMenu(s *discordgo.Session, i *discordgo.InteractionCreate, prompt, action string, tools []database.Tool) {
	options := make([]discordgo.SelectMenuOption, 0, len(tools)+1)
	for _, t := range tools {
		options = append(options, discordgo.SelectMenuOption{
			Label: toolOptionLabel(t),
			Value: componentID(action, t.ID),
		})
	}
	options = append(options, discordgo.SelectMenuOption{
		Label: "Cancel (do nothing)",
		Value: componentID(action, cancelToken),
	})

	menu := discordgo.SelectMenu{
		CustomID:    action,
		Placeholder: "Choose a tool...",
		MinValues:   intPtr(1),
		MaxValues:   1,
		Options:     options,
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    prompt,
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}},
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

// ---- helpers ----

func optionMap(i *discordgo.InteractionCreate) map[string]string {
	m := make(map[string]string)
	for _, opt := range i.ApplicationCommandData().Options {
		m[opt.Name] = opt.StringValue()
	}
	return m
}

// interactionUserNames returns the invoking user's ID, Discord username, global
// display name, and server nickname, handling guild (Member) and DM (User)
// interactions.
func interactionUserNames(i *discordgo.InteractionCreate) (userID, username, globalName, serverName string) {
	if i.Member != nil && i.Member.User != nil {
		u := i.Member.User
		return u.ID, u.Username, u.GlobalName, i.Member.Nick
	}
	if i.User != nil {
		return i.User.ID, i.User.Username, i.User.GlobalName, ""
	}
	return "", "", "", ""
}

// displayNameOf returns the best display name: server nickname, then global
// name, then Discord username.
func displayNameOf(u database.User) string {
	switch {
	case u.ServerName != "":
		return u.ServerName
	case u.GlobalName != "":
		return u.GlobalName
	default:
		return u.Username
	}
}

// ownerLabel returns the display name of a tool's owner.
func ownerLabel(t database.Tool) string {
	return displayNameOf(t.Owner)
}

// toolOptionLabel returns a select-menu option label for a tool, including its
// owner when known, to disambiguate across owners.
func toolOptionLabel(t database.Tool) string {
	if t.Owner.ID != "" {
		return t.Name + " (" + displayNameOf(t.Owner) + ")"
	}
	return t.Name
}

// resolveComponent returns the action and target (toolID or "cancel") from a
// select-menu or button component interaction.
func resolveComponent(data discordgo.MessageComponentInteractionData) (action, toolOrCancel string, ok bool) {
	custom := data.CustomID
	if data.ComponentType == discordgo.SelectMenuComponent && len(data.Values) > 0 {
		custom = data.Values[0]
	}
	return parseComponentID(custom)
}

func parseComponentID(customID string) (action, toolOrCancel string, ok bool) {
	parts := strings.SplitN(customID, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	switch parts[0] {
	case actionBorrow, actionRemove, actionReturn:
		return parts[0], parts[1], true
	}
	return "", "", false
}

func componentID(action, toolOrCancel string) string {
	return action + ":" + toolOrCancel
}

func fuzzyMatchTools(tools []database.Tool, query string) []database.Tool {
	var out []database.Tool
	for _, t := range tools {
		if fuzzy.Match(query, t.Name) {
			out = append(out, t)
		}
	}
	return out
}

func availableTools(tools []database.Tool) []database.Tool {
	var out []database.Tool
	for _, t := range tools {
		if t.BorrowerID == nil || *t.BorrowerID == "" {
			out = append(out, t)
		}
	}
	return out
}

func statusText(t database.Tool) string {
	if t.BorrowerID != nil && *t.BorrowerID != "" {
		return ":lock: borrowed"
	}
	return ":white_check_mark: available"
}

func intPtr(v int) *int { return &v }
