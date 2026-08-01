package provider

import (
	"encoding/json"

	blacklistcapability "github.com/abd-im/abd-im-cli/internal/capability/blacklist"
	conversationcapability "github.com/abd-im/abd-im-cli/internal/capability/conversation"
	friendcapability "github.com/abd-im/abd-im-cli/internal/capability/friend"
	groupcapability "github.com/abd-im/abd-im-cli/internal/capability/group"
	messagecapability "github.com/abd-im/abd-im-cli/internal/capability/message"
	"github.com/abd-im/abd-im-cli/internal/mcp/owner"
)

// DefaultTools maps the fixed typed-method registry to provider MCP names.
// The supplied methods are already a construction-time authorization snapshot;
// unknown methods intentionally have no MCP representation.
func DefaultTools(methods []string) []Tool {
	allowed := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		allowed[method] = struct{}{}
	}
	tools := make([]Tool, 0, len(allowed))
	for _, tool := range owner.DefaultTools() {
		if _, exists := allowed[tool.Method]; !exists {
			continue
		}
		tools = append(tools, Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Method:      tool.Method,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
			Visible:     func() bool { return true },
		})
	}
	if _, exists := allowed[groupcapability.Method]; exists {
		tools = append(tools, Tool{
			Name:        "abdim." + groupcapability.Method,
			Description: "Create a group with approved members.",
			Method:      groupcapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1},"member_ids":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string"}},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["name","member_ids","idempotency_key"]}`),
			Visible:     func() bool { return true },
		})
	}
	for _, method := range []string{groupcapability.JoinMethod, groupcapability.LeaveMethod, groupcapability.InviteMembersMethod, groupcapability.RemoveMembersMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := groupMembershipTool(method)
		tools = append(tools, Tool{Name: "abdim." + method, Description: description, Method: method, InputSchema: schema, Visible: func() bool { return true }})
	}
	for _, method := range []string{groupcapability.SetInfoMethod, groupcapability.SetMuteMethod, groupcapability.SetMemberMuteMethod, groupcapability.TransferOwnerMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := groupAdministrationTool(method)
		tools = append(tools, Tool{Name: "abdim." + method, Description: description, Method: method, InputSchema: schema, Visible: func() bool { return true }})
	}
	if _, exists := allowed[messagecapability.Method]; exists {
		tools = append(tools, Tool{
			Name:        "abdim." + messagecapability.Method,
			Description: "Send a text message to an approved user or group.",
			Method:      messagecapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["text","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`),
			Visible:     func() bool { return true },
		})
	}
	if _, exists := allowed[messagecapability.AtMethod]; exists {
		tools = append(tools, Tool{
			Name:        "abdim." + messagecapability.AtMethod,
			Description: "Send a text message that mentions approved users in an approved group.",
			Method:      messagecapability.AtMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"group_id":{"type":"string","minLength":1},"mention_user_ids":{"type":"array","minItems":1,"maxItems":10,"uniqueItems":true,"items":{"type":"string","minLength":1}},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["text","group_id","mention_user_ids","idempotency_key"]}`),
			Visible:     func() bool { return true },
		})
	}
	if _, exists := allowed[messagecapability.QuoteMethod]; exists {
		tools = append(tools, Tool{
			Name:        "abdim." + messagecapability.QuoteMethod,
			Description: "Reply to one approved message in an approved conversation.",
			Method:      messagecapability.QuoteMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","minLength":1,"maxLength":4096},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"conversation_id":{"type":"string","minLength":1},"message_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["text","conversation_id","message_id","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`),
			Visible:     func() bool { return true },
		})
	}
	if _, exists := allowed[messagecapability.LocationMethod]; exists {
		tools = append(tools, Tool{
			Name: "abdim." + messagecapability.LocationMethod, Description: "Send a location message to an approved user or group.", Method: messagecapability.LocationMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"description":{"type":"string","maxLength":512},"longitude":{"type":"number","minimum":-180,"maximum":180},"latitude":{"type":"number","minimum":-90,"maximum":90},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["longitude","latitude","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`), Visible: func() bool { return true },
		})
	}
	if _, exists := allowed[messagecapability.CustomMethod]; exists {
		tools = append(tools, Tool{
			Name: "abdim." + messagecapability.CustomMethod, Description: "Send a bounded custom message to an approved user or group.", Method: messagecapability.CustomMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"data":{"type":"string","minLength":1,"maxLength":4096},"extension":{"type":"string","maxLength":1024},"description":{"type":"string","maxLength":512},"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["data","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`), Visible: func() bool { return true },
		})
	}
	if _, exists := allowed[messagecapability.RevokeMethod]; exists {
		tools = append(tools, Tool{
			Name: "abdim." + messagecapability.RevokeMethod, Description: "Revoke one approved message sent by the profile owner.", Method: messagecapability.RevokeMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"message_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","message_id","idempotency_key"]}`), Visible: func() bool { return true },
		})
	}
	for _, method := range []string{messagecapability.ImageMethod, messagecapability.FileMethod, messagecapability.SoundMethod, messagecapability.VideoMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := mediaTool(method)
		tools = append(tools, Tool{Name: "abdim." + method, Description: description, Method: method, InputSchema: schema, Visible: func() bool { return true }})
	}
	if _, exists := allowed[conversationcapability.Method]; exists {
		tools = append(tools, Tool{
			Name:        "abdim." + conversationcapability.Method,
			Description: "Mark an approved message boundary as read in one conversation.",
			Method:      conversationcapability.Method,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"up_to_message_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","up_to_message_id","idempotency_key"]}`),
			Visible:     func() bool { return true },
		})
	}
	if _, exists := allowed[conversationcapability.SetPinnedMethod]; exists {
		tools = append(tools, Tool{
			Name: "abdim." + conversationcapability.SetPinnedMethod, Description: "Set the pinned state for one approved conversation.", Method: conversationcapability.SetPinnedMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"pinned":{"type":"boolean"},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","pinned","idempotency_key"]}`), Visible: func() bool { return true },
		})
	}
	if _, exists := allowed[conversationcapability.SetReceiveOptionMethod]; exists {
		tools = append(tools, Tool{
			Name: "abdim." + conversationcapability.SetReceiveOptionMethod, Description: "Set one fixed receive option for an approved conversation.", Method: conversationcapability.SetReceiveOptionMethod,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":256},"option":{"type":"string","enum":["receive","do_not_receive","receive_no_notify"]},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["conversation_id","option","idempotency_key"]}`), Visible: func() bool { return true },
		})
	}
	for _, method := range []string{friendcapability.RequestMethod, friendcapability.RespondMethod, friendcapability.DeleteMethod, friendcapability.SetRemarkMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		description, schema := friendTool(method)
		tools = append(tools, Tool{Name: "abdim." + method, Description: description, Method: method, InputSchema: schema, Visible: func() bool { return true }})
	}
	for _, method := range []string{blacklistcapability.AddMethod, blacklistcapability.RemoveMethod} {
		if _, exists := allowed[method]; !exists {
			continue
		}
		tools = append(tools, Tool{Name: "abdim." + method, Description: "Update one approved blacklist relationship.", Method: method, InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","idempotency_key"]}`), Visible: func() bool { return true }})
	}
	return tools
}

func friendTool(method string) (string, json.RawMessage) {
	switch method {
	case friendcapability.RequestMethod:
		return "Send a friend request to one approved user.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"message":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","idempotency_key"]}`)
	case friendcapability.RespondMethod:
		return "Accept or reject one pending friend request.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"response":{"type":"string","enum":["accept","reject"]},"message":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","response","idempotency_key"]}`)
	case friendcapability.SetRemarkMethod:
		return "Set one approved friend remark.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"remark":{"type":"string","maxLength":128},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","remark","idempotency_key"]}`)
	default:
		return "Delete one approved friend relationship.", json.RawMessage(`{"type":"object","properties":{"user_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["user_id","idempotency_key"]}`)
	}
}

func groupMembershipTool(method string) (string, json.RawMessage) {
	switch method {
	case groupcapability.JoinMethod:
		return "Request to join one approved group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"message":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","idempotency_key"]}`)
	case groupcapability.LeaveMethod:
		return "Leave one approved group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","idempotency_key"]}`)
	case groupcapability.InviteMembersMethod:
		return "Invite approved users to one approved group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"user_ids":{"type":"array","minItems":1,"maxItems":50,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":256}},"reason":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","user_ids","idempotency_key"]}`)
	default:
		return "Remove approved users from one approved group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"user_ids":{"type":"array","minItems":1,"maxItems":50,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":256}},"reason":{"type":"string","maxLength":512},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","user_ids","idempotency_key"]}`)
	}
}

func groupAdministrationTool(method string) (string, json.RawMessage) {
	switch method {
	case groupcapability.SetInfoMethod:
		return "Update approved group profile fields.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"name":{"type":"string","minLength":1,"maxLength":256},"notification":{"type":"string","maxLength":1024},"introduction":{"type":"string","maxLength":1024},"face_url":{"type":"string","maxLength":2048},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","idempotency_key"],"anyOf":[{"required":["name"]},{"required":["notification"]},{"required":["introduction"]},{"required":["face_url"]}]}`)
	case groupcapability.SetMuteMethod:
		return "Set all-member mute for one approved group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"muted":{"type":"boolean"},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","muted","idempotency_key"]}`)
	case groupcapability.SetMemberMuteMethod:
		return "Set mute for one approved member in an approved group.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"user_id":{"type":"string","minLength":1,"maxLength":256},"muted":{"type":"boolean"},"duration_seconds":{"type":"integer","minimum":1,"maximum":604800},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","user_id","muted","idempotency_key"],"oneOf":[{"properties":{"muted":{"const":true}},"required":["duration_seconds"]},{"properties":{"muted":{"const":false}},"not":{"required":["duration_seconds"]}}]}`)
	default:
		return "Transfer ownership of one approved group to an approved member.", json.RawMessage(`{"type":"object","properties":{"group_id":{"type":"string","minLength":1,"maxLength":256},"new_owner_user_id":{"type":"string","minLength":1,"maxLength":256},"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["group_id","new_owner_user_id","idempotency_key"]}`)
	}
}

func mediaTool(method string) (string, json.RawMessage) {
	target := `"recipient_id":{"type":"string","minLength":1},"group_id":{"type":"string","minLength":1}`
	switch method {
	case messagecapability.ImageMethod:
		return "Send one approved image attachment to an approved user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	case messagecapability.FileMethod:
		return "Send one approved file attachment to an approved user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	case messagecapability.SoundMethod:
		return "Send one approved sound attachment to an approved user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},"duration_seconds":{"type":"integer","minimum":1,"maximum":14400},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","duration_seconds","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	default:
		return "Send one approved video attachment and image thumbnail to an approved user or group.", json.RawMessage(`{"type":"object","properties":{"attachment_ref":{"type":"string","minLength":8,"maxLength":128},"file_name":{"type":"string","minLength":1,"maxLength":255},"duration_seconds":{"type":"integer","minimum":1,"maximum":14400},"thumbnail_ref":{"type":"string","minLength":8,"maxLength":128},"thumbnail_file_name":{"type":"string","minLength":1,"maxLength":255},` + target + `,"idempotency_key":{"type":"string","minLength":1,"maxLength":128}},"required":["attachment_ref","file_name","duration_seconds","thumbnail_ref","thumbnail_file_name","idempotency_key"],"oneOf":[{"required":["recipient_id"]},{"required":["group_id"]}]}`)
	}
}
