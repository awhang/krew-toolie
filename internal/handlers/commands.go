package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"krew-toolie/internal/database"
	"krew-toolie/internal/database/repository"
	"krew-toolie/internal/fuzzy"
)

// Component custom IDs encode the action performed on a tool, e.g.
// "borrow:3f8a..." or "remove:3f8a...".
const (
	componentActionBorrow = "borrow"
	componentActionRemove = "remove"
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

// interactionUserID returns the invoking user's ID, handling both guild
// (Member) and DM (User) interactions.
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// HandleInteraction routes slash command interactions to their handlers.
func (h *CommandHandler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()

	// The caller is always captured so users are registered on any command use.
	callerID := interactionUserID(i)
	caller, err := h.userRepo.GetOrCreate(context.Background(), callerID, displayName(i))
	if err != nil {
		h.respondError(s, i, "Database error. Please try again later.")
		return
	}

	// borrow passes explicit user lookup through handleBorrow, so the caller's
	// user is only used as the borrower there.
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

// HandleComponentInteraction processes button clicks (e.g., borrowing one of
// several matching tools).
func (h *CommandHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	action, toolID, ok := parseComponentID(data.CustomID)
	if !ok {
		return
	}

	callerID := interactionUserID(i)
	caller, err := h.userRepo.GetOrCreate(context.Background(), callerID, displayName(i))
	if err != nil {
		h.respondEphemeralError(s, i, "Database error. Please try again later.")
		return
	}

	switch action {
	case componentActionBorrow:
		h.borrowFromButton(s, i, toolID, caller)
	case componentActionRemove:
		h.removeFromButton(s, i, toolID, caller)
	}
}

func (h *CommandHandler) borrowFromButton(s *discordgo.Session, i *discordgo.InteractionCreate, toolID string, caller *database.User) {
	tool, err := h.toolRepo.GetByID(context.Background(), toolID)
	if err != nil {
		h.respondEphemeralError(s, i, "Could not find that tool.")
		return
	}
	if tool == nil {
		h.respondEphemeralError(s, i, "That tool no longer exists.")
		return
	}

	if err := h.toolRepo.Borrow(context.Background(), toolID, caller.ID); err != nil {
		h.respondEphemeralError(s, i, err.Error())
		return
	}

	h.respondEphemeral(s, i, fmt.Sprintf("You've borrowed **%s**!", tool.Name))
}

func (h *CommandHandler) removeFromButton(s *discordgo.Session, i *discordgo.InteractionCreate, toolID string, caller *database.User) {
	if err := h.toolRepo.Remove(context.Background(), toolID, caller.ID); err != nil {
		h.respondEphemeralError(s, i, err.Error())
		return
	}

	h.respondEphemeral(s, i, "Tool removed from your collection.")
}

func (h *CommandHandler) handleAddTool(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	name := strings.TrimSpace(options["name"])
	if name == "" {
		h.respondError(s, i, "Tool name cannot be empty.")
		return
	}

	var storeLink *string
	if link := strings.TrimSpace(options["store_link"]); link != "" {
		storeLink = &link
	}

	exists, err := h.toolRepo.ExistsByName(context.Background(), name, caller.ID)
	if err != nil {
		h.respondError(s, i, "Could not check your tools.")
		return
	}
	if exists {
		h.respondError(s, i, fmt.Sprintf("You already have a tool named **%s**.", name))
		return
	}

	tool := &database.Tool{
		Name:      name,
		StoreLink: storeLink,
		OwnerID:   caller.ID,
	}
	if err := h.toolRepo.Create(context.Background(), tool); err != nil {
		h.respondError(s, i, "Failed to add tool.")
		return
	}

	h.respond(s, i, fmt.Sprintf("Tool **%s** added to your collection!", name))
}

func (h *CommandHandler) handleBorrow(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	userName := strings.TrimSpace(options["user_name"])
	toolName := strings.TrimSpace(options["tool_name"])

	users, err := h.userRepo.FindByUsernameFuzzy(context.Background(), userName)
	if err != nil {
		h.respondError(s, i, "Could not search for users.")
		return
	}
	if len(users) == 0 {
		h.respondError(s, i, fmt.Sprintf("No user found matching **%s**.", userName))
		return
	}

	// If tool_name was not provided, list all of that user's tools.
	if toolName == "" {
		h.listUsersTools(s, i, users)
		return
	}

	// Resolve a single user to borrow from.
	target, more := pickUser(users)
	if more {
		h.respondUsersAmbiguous(s, i, users)
		return
	}

	tools, err := h.toolRepo.GetByOwner(context.Background(), target.ID)
	if err != nil {
		h.respondError(s, i, "Could not fetch tools for that user.")
		return
	}

	matches := fuzzyMatchTools(tools, toolName)
	if len(matches) == 0 {
		h.respondError(s, i, fmt.Sprintf("No tool matching **%s** was found for **%s**.", toolName, target.Username))
		return
	}

	available := availableTools(matches)
	if len(available) == 0 {
		h.respondError(s, i, "That tool is already borrowed.")
		return
	}

	if len(available) == 1 {
		tool := available[0]
		if err := h.toolRepo.Borrow(context.Background(), tool.ID, caller.ID); err != nil {
			h.respondError(s, i, err.Error())
			return
		}
		h.respond(s, i, fmt.Sprintf("You've borrowed **%s** from %s!", tool.Name, target.Username))
		return
	}

	// Multiple available matches: show interactive buttons to choose.
	h.respondChooseButtons(s, i, fmt.Sprintf("Multiple tools match **%s** for %s. Choose one:", toolName, target.Username), available, componentActionBorrow)
}

func (h *CommandHandler) handleReturn(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	toolName := strings.TrimSpace(options["tool_name"])

	if err := h.toolRepo.ReturnByName(context.Background(), toolName); err != nil {
		h.respondError(s, i, err.Error())
		return
	}

	h.respond(s, i, fmt.Sprintf("**%s** has been returned!", toolName))
}

func (h *CommandHandler) handleRemoveTool(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	options := optionMap(i)
	toolName := strings.TrimSpace(options["tool_name"])
	if toolName == "" {
		h.respondError(s, i, "Tool name cannot be empty.")
		return
	}

	tools, err := h.toolRepo.GetByOwner(context.Background(), caller.ID)
	if err != nil {
		h.respondError(s, i, "Could not fetch your tools.")
		return
	}

	matches := fuzzyMatchTools(tools, toolName)
	if len(matches) == 0 {
		h.respondError(s, i, fmt.Sprintf("No tool matching **%s** was found in your collection.", toolName))
		return
	}

	if len(matches) == 1 {
		if err := h.toolRepo.Remove(context.Background(), matches[0].ID, caller.ID); err != nil {
			h.respondError(s, i, err.Error())
			return
		}
		h.respond(s, i, fmt.Sprintf("Removed **%s** from your collection.", matches[0].Name))
		return
	}

	// Multiple matches: show interactive buttons to choose which to remove.
	h.respondChooseButtons(s, i, fmt.Sprintf("Multiple tools match **%s**. Choose one to remove:", toolName), matches, componentActionRemove)
}

func (h *CommandHandler) handleMyTools(s *discordgo.Session, i *discordgo.InteractionCreate, caller *database.User) {
	tools, err := h.toolRepo.GetByOwner(context.Background(), caller.ID)
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
		b.WriteString(fmt.Sprintf("- %s — %s\n", t.Name, statusText(t)))
	}

	h.respond(s, i, b.String())
}

func (h *CommandHandler) handleAvailable(s *discordgo.Session, i *discordgo.InteractionCreate) {
	tools, err := h.toolRepo.GetAvailable(context.Background())
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

// listUsersTools responds with the tools owned by the matched users.
func (h *CommandHandler) listUsersTools(s *discordgo.Session, i *discordgo.InteractionCreate, users []database.User) {
	target, more := pickUser(users)
	if more {
		h.respondUsersAmbiguous(s, i, users)
		return
	}

	tools, err := h.toolRepo.GetByOwner(context.Background(), target.ID)
	if err != nil {
		h.respondError(s, i, "Could not fetch tools for that user.")
		return
	}

	if len(tools) == 0 {
		h.respond(s, i, fmt.Sprintf("**%s** has no tools listed.", target.Username))
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%s's tools:**\n", target.Username))
	for _, t := range tools {
		b.WriteString(fmt.Sprintf("- %s — %s\n", t.Name, statusText(t)))
	}

	h.respond(s, i, b.String())
}

func (h *CommandHandler) respondUsersAmbiguous(s *discordgo.Session, i *discordgo.InteractionCreate, users []database.User) {
	var names []string
	for _, u := range users {
		names = append(names, u.Username)
	}
	h.respondError(s, i, fmt.Sprintf("Found multiple users matching: %s. Please be more specific.", strings.Join(names, ", ")))
}

// respondChooseButtons responds ephemerally with a header and one button per
// tool, each labelled with the tool name.
func (h *CommandHandler) respondChooseButtons(s *discordgo.Session, i *discordgo.InteractionCreate, header string, tools []database.Tool, action string) {
	const maxButtonsPerRow = 5

	var rows []discordgo.MessageComponent
	var row []discordgo.MessageComponent
	for idx, t := range tools {
		row = append(row, discordgo.Button{
			Label:    t.Name,
			Style:    discordgo.PrimaryButton,
			CustomID: componentID(action, t.ID),
		})
		if len(row) == maxButtonsPerRow || idx == len(tools)-1 {
			rows = append(rows, discordgo.ActionsRow{Components: row})
			row = nil
		}
	}

	resp := &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    header,
			Components: rows,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	}
	s.InteractionRespond(i.Interaction, resp)
}

// respond sends a public message to the channel.
func (h *CommandHandler) respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
}

func (h *CommandHandler) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	h.respond(s, i, "❌ "+message)
}

// respondEphemeral sends an ephemeral message.
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

// --- helpers ---

func optionMap(i *discordgo.InteractionCreate) map[string]string {
	m := make(map[string]string)
	for _, opt := range i.ApplicationCommandData().Options {
		m[opt.Name] = opt.StringValue()
	}
	return m
}

func displayName(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.Username
	}
	if i.User != nil {
		return i.User.Username
	}
	return ""
}

// fuzzyMatchTools returns tools whose name fuzzy-matches query.
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

// pickUser returns the single user and false, or returns false+true indicating
// the match is ambiguous (more than one user).
func pickUser(users []database.User) (database.User, bool) {
	if len(users) == 1 {
		return users[0], false
	}
	return database.User{}, true
}

func componentID(action, toolID string) string {
	return action + ":" + toolID
}

func parseComponentID(customID string) (action, toolID string, ok bool) {
	parts := strings.SplitN(customID, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	switch parts[0] {
	case componentActionBorrow, componentActionRemove:
		return parts[0], parts[1], true
	}
	return "", "", false
}
