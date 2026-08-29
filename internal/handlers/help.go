package handlers

import (
	"github.com/bwmarrin/discordgo"
)

// handleToolieHelp shows an ephemeral quick-reference embed listing every
// command, its arguments, and what it does — meant for new users.
func (h *CommandHandler) handleToolieHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "🛠️ Toolie - Quick Reference",
		Description: "Share and borrow tools with your community.",
		Color:       0x00aaff,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "`/addtool name [store_link]`",
				Value: "Register one of your tools so others can borrow it. The link is optional.",
			},
			{
				Name:  "`/borrow tool_name [owner]`",
				Value: "Borrow a tool by name. Add an owner (name or nickname) to narrow it. Single matches confirm first; multiple matches show a picker.",
			},
			{
				Name:  "`/return tool_name`",
				Value: "Return a tool you currently have borrowed.",
			},
			{
				Name:  "`/removetool tool_name`",
				Value: "Remove one of your own tools from your collection.",
			},
			{
				Name:  "`/mytools`",
				Value: "List the tools you currently own.",
			},
			{
				Name:  "`/available [tool]`",
				Value: "List tools you can borrow (excluding your own), grouped by owner. Add a tool name to filter.",
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Tool search is fuzzy: e.g. 'snow blower' matches 'Ego Snow Blower'.",
		},
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}
