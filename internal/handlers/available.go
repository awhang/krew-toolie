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
	"krew-toolie/internal/fuzzy"
)

// handleAvailable lists tools the caller can borrow (excluding their own),
// grouped by owner, in a paginated embed.
func (h *CommandHandler) handleAvailable(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := optionMap(i)
	filter := strings.TrimSpace(options["tool"])
	log.Printf("handling /available (filter=%q)", filter)

	groups, err := h.availableGroups(context.Background(), i, filter)
	if err != nil {
		log.Printf("availableGroups error: %v", err)
		h.respondEphemeralError(s, i, "Could not fetch available tools.")
		return
	}
	if len(groups) == 0 {
		log.Printf("/available: no groups to show")
		h.respondEphemeral(s, i, "No tools are currently available to borrow.")
		return
	}

	log.Printf("/available: rendering %d owner groups", len(groups))
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
		value := strings.Join(lines, "\n")
		if len(value) > 1000 {
			// Discord caps embed field values at 1024 chars; overflowing it causes
			// the whole message to be rejected (silent non-response). Truncate.
			log.Printf("truncating owner field for %q (%d chars)", displayNameOf(g.owner), len(value))
			value = value[:1000] + "\n…"
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  displayNameOf(g.owner),
			Value: value,
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
		log.Printf("Failed to render /available page (update=%v): %v", update, err)
		return
	}
	log.Printf("/available: responded with page %d of %d (embeds=%d, components=%d)", page+1, totalPages, len(embed.Fields), len(components))
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

// pageButton builds a pagination button that targets a specific page and
// carries the current filter query so the page can be rebuilt on click.
func (h *CommandHandler) pageButton(totalTools int, label string, targetPage int, filter string, disabled bool) discordgo.Button {
	return discordgo.Button{
		Label:    label,
		Style:    discordgo.PrimaryButton,
		CustomID: prefixAvailPage + strconv.Itoa(targetPage) + ":" + base64.RawURLEncoding.EncodeToString([]byte(filter)),
		Disabled: disabled,
	}
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
