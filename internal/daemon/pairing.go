package daemon

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/abd-im/abd-im-cli/internal/contracts"
)

type PairingConfig struct {
	BotUserID   string
	OwnerUserID string
	CodeHash    string
	ExpiresAt   time.Time
	BindOwner   func(string) error
	Send        func(context.Context, string, string) error
	Now         func() time.Time
}

// Pairing consumes setup messages before normal inbound policy evaluation and
// exposes the single canonical owner accepted after pairing.
type Pairing struct {
	botUserID string
	codeHash  []byte
	expiresAt time.Time
	bindOwner func(string) error
	send      func(context.Context, string, string) error
	now       func() time.Time

	mu      sync.RWMutex
	ownerID string
}

func NewPairing(config PairingConfig) (*Pairing, error) {
	if strings.TrimSpace(config.BotUserID) == "" || config.BindOwner == nil || config.Send == nil {
		return nil, errors.New("bot user ID, owner binder, and pairing sender are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	pairing := &Pairing{
		botUserID: strings.TrimSpace(config.BotUserID),
		ownerID:   strings.TrimSpace(config.OwnerUserID),
		expiresAt: config.ExpiresAt,
		bindOwner: config.BindOwner,
		send:      config.Send,
		now:       config.Now,
	}
	if pairing.ownerID != "" {
		if config.CodeHash != "" || !config.ExpiresAt.IsZero() {
			return nil, errors.New("paired owner cannot retain a pending code")
		}
		return pairing, nil
	}
	decoded, err := hex.DecodeString(config.CodeHash)
	if err != nil || len(decoded) != sha256.Size || config.ExpiresAt.IsZero() {
		return nil, errors.New("valid pending pairing code and expiry are required")
	}
	pairing.codeHash = decoded
	return pairing, nil
}

func (p *Pairing) Handle(ctx context.Context, event contracts.SDKEvent) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ownerID != "" {
		return false, nil
	}
	reference, ok := pairingReference(event)
	if !ok || reference.SenderID == p.botUserID {
		return true, nil
	}
	if reference.SessionType != 1 || !p.now().Before(p.expiresAt) {
		return true, nil
	}
	code, found := strings.CutPrefix(strings.ToUpper(strings.TrimSpace(event.MessageText)), "PAIR ")
	if !found {
		return true, nil
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(code)))
	if subtle.ConstantTimeCompare(p.codeHash, digest[:]) != 1 {
		return true, nil
	}
	if err := p.bindOwner(reference.SenderID); err != nil {
		return true, err
	}
	p.ownerID = reference.SenderID
	p.codeHash = nil
	if err := p.send(ctx, "Pairing complete. You can now use all available IM capabilities.", reference.SenderID); err != nil {
		return true, err
	}
	return true, nil
}

func (p *Pairing) Accept(event contracts.SDKEvent) bool {
	reference, ok := pairingReference(event)
	if !ok {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ownerID != "" && reference.SenderID == p.ownerID
}

func pairingReference(event contracts.SDKEvent) (struct {
	SenderID    string `json:"sender_id"`
	SessionType int32  `json:"session_type"`
}, bool) {
	var reference struct {
		SenderID    string `json:"sender_id"`
		SessionType int32  `json:"session_type"`
	}
	if event.Type != string(contracts.EventMessageReceived) || json.Unmarshal(event.Data, &reference) != nil || strings.TrimSpace(reference.SenderID) == "" {
		return reference, false
	}
	return reference, true
}
