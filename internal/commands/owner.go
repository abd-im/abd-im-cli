package commands

// All returns the complete CLI command surface.
func All() []string {
	return []string{
		"profile.get",
		"user.me",
		"user.get",
		"daemon.status",
		"doctor.get",
		"conversation.list",
		"conversation.get",
		"conversation.search",
		"message.history",
		"message.search",
		"message.get",
		"message.send",
		"group.list",
		"group.get",
		"group.search",
		"group.members.list",
		"group.members.search",
		"friend.list",
		"friend.get",
		"friend.search",
		"blacklist.list",
		"blacklist.get",
	}
}
