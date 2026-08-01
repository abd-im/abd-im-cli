package capability

import (
	"errors"
	"strings"
)

const compatibilityNotValidatedReason = "provider/SDK/server compatibility is not validated"

// SingleCodexOpenIMCompatibility is the controlled compatibility record for
// the only currently supported provider deployment. Updating any member
// requires a new compatibility gate result before daemon manifests may expose
// static available capabilities.
var SingleCodexOpenIMCompatibility = Compatibility{
	Provider:    "codex",
	MCPProtocol: "2026-07-28",
	SDKVersion:  "3.8.0",
	ServerAPI:   "openim-api/v3",
}

// Compatibility identifies one exact provider, protocol, SDK, and server API
// combination that has passed a controlled compatibility gate.
type Compatibility struct {
	Provider    string
	MCPProtocol string
	SDKVersion  string
	ServerAPI   string
}

func (c Compatibility) valid() bool {
	return strings.TrimSpace(c.Provider) != "" &&
		strings.TrimSpace(c.MCPProtocol) != "" &&
		strings.TrimSpace(c.SDKVersion) != "" &&
		strings.TrimSpace(c.ServerAPI) != ""
}

// EvidenceGate keeps the capability surface closed unless the runtime
// combination exactly matches recorded compatibility evidence.
type EvidenceGate struct {
	supported map[Compatibility]struct{}
}

// NewEvidenceGate creates a gate from controlled compatibility evidence.
func NewEvidenceGate(combinations []Compatibility) (*EvidenceGate, error) {
	if len(combinations) == 0 {
		return nil, errors.New("at least one compatibility combination is required")
	}
	gate := &EvidenceGate{supported: make(map[Compatibility]struct{}, len(combinations))}
	for _, combination := range combinations {
		if !combination.valid() {
			return nil, errors.New("provider, MCP protocol, SDK version, and server API are required")
		}
		if _, exists := gate.supported[combination]; exists {
			return nil, errors.New("duplicate compatibility combination")
		}
		gate.supported[combination] = struct{}{}
	}
	return gate, nil
}

// Supports reports whether the exact combination has recorded evidence.
func (g *EvidenceGate) Supports(combination Compatibility) bool {
	if g == nil || !combination.valid() {
		return false
	}
	_, ok := g.supported[combination]
	return ok
}

// Entries returns a copied manifest input. Static available entries are
// downgraded unless the exact runtime combination has recorded evidence.
func (g *EvidenceGate) Entries(combination Compatibility, entries []Entry) []Entry {
	filtered := append([]Entry(nil), entries...)
	if g.Supports(combination) {
		return filtered
	}
	for index := range filtered {
		if filtered[index].Status == Available {
			filtered[index].Status = NotValidated
			filtered[index].Reason = compatibilityNotValidatedReason
		}
	}
	return filtered
}

// Manifest applies compatibility evidence before constructing a manifest.
// It never upgrades a non-available static entry.
func (g *EvidenceGate) Manifest(combination Compatibility, entries []Entry) (*Manifest, error) {
	return New(g.Entries(combination, entries))
}
