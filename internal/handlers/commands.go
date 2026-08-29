package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"strconv"
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
// for borrow/remove/return confirmation, /available pagination, and
// borrow-from-available.
func (h *CommandHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	data := i.MessageComponentData()

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

// parseAvailPageID decodes a pagination button custom ID of the form
// "availpage:<page>:<b64filter>".
func parseAvailPageID(id string) (page int, filter string, ok bool) {
	rest := strings.TrimPrefix(id, prefixAvailPage)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	p, err := strconv.Atoi(parts[0])
	if err != nil || p < 0 {
		return 0, "", false
	}
	filterBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, "", false
	}
	return p, string(filterBytes), true
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
	options := optionMap(i)
	filter := strings.TrimSpace(options["tool"])

	groups, err := h.availableGroups(context.Background(), i, filter)
	if err != nil {
		h.respondEphemeralError(s, i, "Could not fetch available tools.")
		return
	}
	if len(groups) == 0 {
		h.respondEphemeral(s, i, "No tools are currently available to borrow.")
		return
	}

	h.renderAvailablePage(s, i, groups, 0, filter, false)
}

// ownerGroup groups the available tools owned by a single user.
type ownerGroup struct {
	owner database.User
	tools []database.Tool // sorted by name
}

// availableGroups returns available tools (not the caller's own) grouped by
// owner, optionally fuzzy-filtered by name. Owners are sorted by display name,
// then tools within each owner alphabetically.
func (h *CommandHandler) availableGroups(ctx context.Context, i *discordgo.InteractionCreate, filter string) ([]ownerGroup, error) {
	callerID, _, _, _ := interactionUserNames(i)
	tools, err := h.toolRepo.GetAvailable(ctx)
	if err != nil {
		return nil, err
	}

	byOwner := make(map[string]*ownerGroup)
	var order []string
	for _, t := range tools {
		if t.Owner.ID == "" || t.Owner.ID == callerID {
			continue // own tool: cannot borrow your own
		}
		if filter != "" && !fuzzy.Match(filter, t.Name) {
			continue
		}
		g := byOwner[t.Owner.ID]
		if g == nil {
			own := t.Owner
			g = &ownerGroup{owner: own}
			byOwner[t.Owner.ID] = g
			order = append(order, t.Owner.ID)
		}
		g.tools = append(g.tools, t)
	}

	groups := make([]ownerGroup, 0, len(order))
	for _, id := range order {
		g := byOwner[id]
		// Sort tools within the owner group alphabetically.
		sort.Slice(g.tools, func(a, b int) bool {
			return strings.ToLower(g.tools[a].Name) < strings.ToLower(g.tools[b].Name)
		})
		groups = append(groups, *g)
	}
	// Sort owners by display name (case-insensitive), then by ID for stability.
	sort.Slice(groups, func(a, b int) bool {
		na, nb := strings.ToLower(displayNameOf(groups[a].owner)), strings.ToLower(displayNameOf(groups[b].owner))
		if na != nb {
			return na < nb
		}
		return groups[a].owner.ID < groups[b].owner.ID
	})
	return groups, nil
}

// renderAvailablePage renders a grouped embed for the current page, with a
// borrow select menu and pagination buttons. When update is true the existing
// message is edited in place (pagination); otherwise a new ephemeral message
// is sent.
func (h *CommandHandler) renderAvailablePage(s *discordgo.Session, i *discordgo.InteractionCreate, groups []ownerGroup, page int, filter string, update bool) {
	totalPages := (len(groups) + ownersPerPage - 1) / ownersPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * ownersPerPage
	end := start + ownersPerPage
	if end > len(groups) {
		end = len(groups)
	}
	slice := groups[start:end]

	embed := &discordgo.MessageEmbed{
		Title:       "🛠️ Available to borrow",
		Description: "Tools you can borrow (your own are excluded).",
		Color:       0x00aaff,
		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Page %d/%d", page+1, totalPages)},
	}
	if filter != "" {
		embed.Description = fmt.Sprintf("Tools you can borrow matching **%s**.", filter)
	}

	for _, g := range slice {
		var lines []string
		for _, t := range g.tools {
			lines = append(lines, "• "+t.Name)
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  displayNameOf(g.owner),
			Value: strings.Join(lines, "\n"),
		})
	}

	components := h.availableComponents(slice, page, totalPages, filter, len(groups))

	var resp *discordgo.InteractionResponse
	if update {
		resp = &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{embed},
				Components: components,
			},
		}
	} else {
		resp = &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{embed},
				Components: components,
				Flags:      discordgo.MessageFlagsEphemeral,
			},
		}
	}
	if err := s.InteractionRespond(i.Interaction, resp); err != nil {
		log.Printf("Failed to render /available page: %v", err)
	}
}

// availableComponents builds the borrow select menu (tools on this page) and
// the pagination button row.
func (h *CommandHandler) availableComponents(pageTools []ownerGroup, page, totalPages int, filter string, totalTools int) []discordgo.MessageComponent {
	var comps []discordgo.MessageComponent

	// Borrow select menu for the tools on this page.
	selectOptions := make([]discordgo.SelectMenuOption, 0, 24)
	for _, g := range pageTools {
		for _, t := range g.tools {
			if len(selectOptions) >= 25 {
				break
			}
			selectOptions = append(selectOptions, discordgo.SelectMenuOption{
				Label: t.Name + " (" + displayNameOf(g.owner) + ")",
				Value: prefixAvailBorrow + t.ID,
			})
		}
		if len(selectOptions) >= 25 {
			break
		}
	}
	if len(selectOptions) > 0 {
		selectOptions = append(selectOptions, discordgo.SelectMenuOption{
			Label: "Cancel",
			Value: prefixAvailBorrow + cancelToken,
		})
		menu := discordgo.SelectMenu{
			CustomID:    "availborrow",
			Placeholder: "Borrow a tool from this list...",
			MinValues:   intPtr(1),
			MaxValues:   1,
			Options:     selectOptions,
		}
		comps = append(comps, discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}})
	}

	// Pagination buttons: First, Prev, counter, Next, Last.
	counter := discordgo.Button{
		Label:    fmt.Sprintf("%d/%d", page+1, totalPages),
		Style:    discordgo.SecondaryButton,
		CustomID: "availnone",
		Disabled: true,
	}
	first := h.pageButton(totalTools, "⏮", 0, filter, page == 0)
	prev := h.pageButton(totalTools, "◀", page-1, filter, page == 0)
	next := h.pageButton(totalTools, "▶", page+1, filter, page >= totalPages-1)
	last := h.pageButton(totalTools, "⏭", totalPages-1, filter, page >= totalPages-1)

	comps = append(comps, discordgo.ActionsRow{Components: []discordgo.MessageComponent{first, prev, counter, next, last}})
	return comps
}

func (h *CommandHandler) pageButton(totalTools int, label string, targetPage int, filter string, disabled bool) discordgo.Button {
	return discordgo.Button{
		Label:    label,
		Style:    discordgo.PrimaryButton,
		CustomID: prefixAvailPage + strconv.Itoa(targetPage) + ":" + base64.RawURLEncoding.EncodeToString([]byte(filter)),
		Disabled: disabled,
	}
}

// borrowFromAvailable starts the existing /borrow confirm flow for a single
// tool chosen from the /available list.
func (h *CommandHandler) borrowFromAvailable(s *discordgo.Session, i *discordgo.InteractionCreate, toolOrCancel string) {
	if toolOrCancel == cancelToken {
		h.respondEphemeral(s, i, "Cancelled.")
		h.deletePrompt(s, i)
		return
	}
	ctx := context.Background()
	tool, err := h.toolRepo.GetByID(ctx, toolOrCancel)
	if err != nil || tool == nil {
		h.respondEphemeralError(s, i, "That tool is no longer available.")
		h.deletePrompt(s, i)
		return
	}
	h.respondConfirm(s, i,
		fmt.Sprintf("Borrow **%s** (owned by %s)?", tool.Name, ownerLabel(*tool)),
		actionBorrow, tool.ID)
	// Remove the /available list since the user is now deciding on that tool.
	h.deletePrompt(s, i)
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
