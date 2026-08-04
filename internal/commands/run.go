package commands

import (
	"encoding/json"

	blacklistcapability "github.com/abd-im/abd-im-cli/internal/capability/blacklist"
	conversationcapability "github.com/abd-im/abd-im-cli/internal/capability/conversation"
	friendcapability "github.com/abd-im/abd-im-cli/internal/capability/friend"
	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
)

// Run maps a run's fixed typed-method snapshot to CLI commands.
// The supplied methods are already a construction-time authorization snapshot;
// unknown methods intentionally have no CLI representation.
func Run(methods []string) []Command {
	allowed := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		allowed[method] = struct{}{}
	}
	commands := make([]Command, 0, len(allowed))
	for _, item := range Owner() {
		if _, exists := allowed[item.Method]; !exists || runForbiddenOwnerMethod(item.Method) {
			continue
		}
		commands = append(commands, Command{
			Description: item.Description,
			Method:      item.Method,
			InputSchema: append(json.RawMessage(nil), item.InputSchema...),
		})
	}
	if _, exists := allowed[groupcapability.Method]; exists {
		commands = append(commands, Command{
			Description: "Create a group with the supplied members.",
			Method:      groupcapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1},"member_ids":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string"}},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["name","member_ids","idempotency_key"]}`),
		})
	}
	for _, method := range []string{groupcapability.JoinMethod, groupcapability.LeaveMethod, groupcapability.InviteMembersMethod, groupcapability.RemoveMembersMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := groupMembershipTool(method)
		commands = append(commands, Command{Description: description, Method: method, InputSchema: schema})
	}
	for _, method := range []string{groupcapability.SetInfoMethod, groupcapability.SetMuteMethod, groupcapability.SetMemberMuteMethod, groupcapability.TransferOwnerMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := groupAdministrationTool(method)
		commands = append(commands, Command{Description: description, Method: method, InputSchema: schema})
	}
	if _, exists := allowed[messagecapability.Method]; exists {
		commands = append(commands, Command{
			Description: "Send a text message to a user or group.",
			Method:      messagecapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["text","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`),
		})
	}
	if _, exists := allowed[messagecapability.AtMethod]; exists {
		commands = append(commands, Command{
			Description: "Send a text message that mentions users in a group.",
			Method:      messagecapability.AtMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"group_id":{"type":"string","minLength":1},"mention_user_ids":{"type":"array","minItems":1,"maxItems":10,"uniqueItems":true,"items":{"type":"string","minLength":1}},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["text","group_id","mention_user_ids","idempotency_key"]}`),
		})
	}
	if _, exists := allowed[messagecapability.QuoteMethod]; exists {
		commands = append(commands, Command{
			Description: "Reply to one message in a conversation.",
			Method:      messagecapability.QuoteMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"conversation_id":{"type":"string","minLength":1},"message_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["text","conversation_id","message_id","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`),
		})
	}
	if _, exists := allowed[messagecapability.LocationMethod]; exists {
		commands = append(commands, Command{
			Description: "Send a location message to a user or group.", Method: messagecapability.LocationMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"description":{"type":"string","maxLength":512},"longitude":{"type":"number","minimum":-180,"maximum":180},"latitude":{"type":"number","minimum":-90,"maximum":90},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["longitude","latitude","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`),
		})
	}
	if _, exists := allowed[messagecapability.CustomMethod]; exists {
		commands = append(commands, Command{
			Description: "Send a bounded custom message to a user or group.", Method: messagecapability.CustomMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"data":{"type":"string","minLength":1,"maxLength":4096},"extension":{"type":"string","maxLength":1024},"description":{"type":"string","maxLength":512},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["data","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`),
		})
	}
	if _, exists := allowed[messagecapability.RevokeMethod]; exists {
		commands = append(commands, Command{
			Description: "Revoke one message sent by the profile owner.", Method: messagecapability.RevokeMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"message_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","message_id","idempotency_key"]}`),
		})
	}
	for _, method := range []string{messagecapability.ImageMethod, messagecapability.FileMethod, messagecapability.SoundMethod, messagecapability.VideoMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := mediaTool(method)
		commands = append(commands, Command{Description: description, Method: method, InputSchema: schema})
	}
	if _, exists := allowed[conversationcapability.Method]; exists {
		commands = append(commands, Command{
			Description: "Mark a message boundary as read in one conversation.",
			Method:      conversationcapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"up_to_message_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","up_to_message_id","idempotency_key"]}`),
		})
	}
	if _, exists := allowed[conversationcapability.SetPinnedMethod]; exists {
		commands = append(commands, Command{
			Description: "Set the pinned state for one conversation.", Method: conversationcapability.SetPinnedMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"pinned":{"type":"boolean"},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","pinned","idempotency_key"]}`),
		})
	}
	if _, exists := allowed[conversationcapability.SetReceiveOptionMethod]; exists {
		commands = append(commands, Command{
			Description: "Set one fixed receive option for a conversation.", Method: conversationcapability.SetReceiveOptionMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"option":{"type":"string","enum":["receive","do_not_receive","receive_no_notify"]},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","option","idempotency_key"]}`),
		})
	}
	for _, method := range []string{friendcapability.RequestMethod, friendcapability.RespondMethod, friendcapability.DeleteMethod, friendcapability.SetRemarkMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := friendTool(method)
		commands = append(commands, Command{Description: description, Method: method, InputSchema: schema})
	}
	for _, method := range []string{blacklistcapability.AddMethod, blacklistcapability.RemoveMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		commands = append(commands, Command{Description: "Update one blacklist relationship.", Method: method, InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","idempotency_key"]}`)})
	}
	return commands
}

// Run commands may reuse typed owner read schemas, but daemon controller and
// diagnostics methods remain owner-only even if an invalid construction
// snapshot names them.
func runForbiddenOwnerMethod(method string) bool {
	switch method {
	case "run.list", "run.cancel", "operation.get", "operation.mark_unknown":
		return true
	default:
		return false
	}
}

func friendTool(method string) (string, json.RawMessage) {
	switch method {
	case friendcapability.RequestMethod:
		return "Send a friend request to one user.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"message":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","idempotency_key"]}`)
	case friendcapability.RespondMethod:
		return "Accept or reject one pending friend request.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"response":{"type":"string","enum":["accept","reject"]},"message":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","response","idempotency_key"]}`)
	case friendcapability.SetRemarkMethod:
		return "Set one friend remark.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"remark":{"type":"string","maxLength":128},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","remark","idempotency_key"]}`)
	default:
		return "Delete one friend relationship.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","idempotency_key"]}`)
	}
}

func groupMembershipTool(method string) (string, json.RawMessage) {
	switch method {
	case groupcapability.JoinMethod:
		return "Request to join one group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"message":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","idempotency_key"]}`)
	case groupcapability.LeaveMethod:
		return "Leave one group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","idempotency_key"]}`)
	case groupcapability.InviteMembersMethod:
		return "Invite users to one group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"user_ids":{"type":"array","minItems":1,"maxItems":50,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":256}},"reason":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","user_ids","idempotency_key"]}`)
	default:
		return "Remove users from one group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"user_ids":{"type":"array","minItems":1,"maxItems":50,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":256}},"reason":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","user_ids","idempotency_key"]}`)
	}
}

func groupAdministrationTool(method string) (string, json.RawMessage) {
	switch method {
	case groupcapability.SetInfoMethod:
		return "Update group profile fields.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"name":{"type":"string","minLength":1,"maxLength":256},"notification":{"type":"string","maxLength":1024},"introduction":{"type":"string","maxLength":1024},"face_url":{"type":"string","maxLength":2048},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","idempotency_key"],"anyOf":[{"required":["name"]},{"required":["notification"]},{"required":["introduction"]},{"required":["face_url"]}]}`)
	case groupcapability.SetMuteMethod:
		return "Set all-member mute for one group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"muted":{"type":"boolean"},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","muted","idempotency_key"]}`)
	case groupcapability.SetMemberMuteMethod:
		return "Set mute for one member in a group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"user_id":{"type":"string","minLength":1,"maxLength":256},"muted":{"type":"boolean"},"duration_seconds":{"type":"integer","minimum":1,"maximum":604800},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","user_id","muted","idempotency_key"],"oneOf":[{"properties":{"muted":{"const":true}},"required":["duration_seconds"]},{"properties":{"muted":{"const":false}},"not":{"required":["duration_seconds"]}}]}`)
	default:
		return "Transfer ownership of one group to a member.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"new_owner_user_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","new_owner_user_id","idempotency_key"]}`)
	}
}

func mediaTool(method string) (string, json.RawMessage) {
	target := `"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1}`
	switch method {
	case messagecapability.ImageMethod:
		return "Send one image attachment to a user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	case messagecapability.FileMethod:
		return "Send one file attachment to a user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	case messagecapability.SoundMethod:
		return "Send one sound attachment to a user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},"duration_seconds":{"type":"integer","minimum":1,"maximum":14400},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","duration_seconds","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	default:
		return "Send one video attachment and image thumbnail to a user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},"duration_seconds":{"type":"integer","minimum":1,"maximum":14400},"thumbnail_ref":{"type":"string","minLength":8,"maxLength":128},"thumbnail_file_name":{"type":"string","minLength":1,"maxLength":255},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","duration_seconds","thumbnail_ref","thumbnail_file_name","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	}
}
