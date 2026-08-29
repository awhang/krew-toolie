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

	// /available uses a paginated group-by-owner embed. Component IDs:
	//   availpage:<page>:<b64filter>   pagination button (targets a page)
	//   availborrow:<toolID>           borrow-select option for one tool
	prefixAvailPage   = "availpage:"
	prefixAvailBorrow = "availborrow:"

	// ownersPerPage controls how many owner groups are shown per page.
	ownersPerPage = 5
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

	// Guard against panics so a single bad command never leaves the user with a
	// silently unresponsive interaction.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in slash command %q: %v", data.Name, r)
			h.respondEphemeralError(s, i, "An internal error occurred while handling that command.")
		}
	}()

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
	case "tooliehelp":
		h.handleToolieHelp(s, i)
	}
}

// HandleComponentInteraction processes button clicks and select-menu choices
// for borrow/remove/return confirmation, /available pagination, and
// borrow-from-available.
func (h *CommandHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	data := i.MessageComponentData()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in component interaction: %v", r)
			h.respondEphemeralError(s, i, "An internal error occurred. Please try again.")
		}
	}()

	// /available pagination buttons: edit the embed in place.
	if strings.HasPrefix(data.CustomID, prefixAvailPage) {
		page, filter, ok := parseAvailPageID(data.CustomID)
		if !ok {
			return
		}
		groups, err := h.availableGroups(context.Background(), i, filter)
		if err != nil {
			h.respondEphemeralError(s, i, "Could not fetch available tools.")
			return
		}
		h.renderAvailablePage(s, i, groups, page, filter, true)
		return
	}

	// /available borrow select: choose a tool from the list to start /borrow.
	if data.ComponentType == discordgo.SelectMenuComponent && data.CustomID == "availborrow" && len(data.Values) > 0 {
		payload := data.Values[0]
		if strings.HasPrefix(payload, prefixAvailBorrow) {
			h.borrowFromAvailable(s, i, strings.TrimPrefix(payload, prefixAvailBorrow))
			return
		}
	}

	action, toolOrCancel, ok := resolveComponent(data)
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

// deletePrompt removes the original message that contained the button(s) or
// select menu after the user has made their choice, since it is no longer
// needed. The prompts are ephemeral, so this deletes the original interaction
// response.
func (h *CommandHandler) deletePrompt(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := s.InteractionResponseDelete(i.Interaction); err != nil {
		log.Printf("Failed to delete prompt message: %v", err)
	}
}

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

// respondOwnersAmbiguous reports that more than one owner matched a query.
func (h *CommandHandler) respondOwnersAmbiguous(s *discordgo.Session, i *discordgo.InteractionCreate, users []database.User) {
	var names []string
	for _, u := range users {
		names = append(names, displayNameOf(u))
	}
	h.respondEphemeralError(s, i, fmt.Sprintf("Found multiple owners matching: %s. Please be more specific.", strings.Join(names, ", ")))
}

// ---- shared helpers ----

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
