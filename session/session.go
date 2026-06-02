package session

import "time"

// Role identifies the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one entry in the persisted conversation history.
type Message struct {
	Role      Role      `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// Session is the complete persisted state of one conversation.
type Session struct {
	ID           string    `json:"id"`            // "2026-06-01T14-32-05"
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	EndpointName string    `json:"endpoint_name"`
	ModelName    string    `json:"model_name"`
	Messages     []Message `json:"messages"`
	TokensUsed   int       `json:"tokens_used"`
	Preview      string    `json:"preview"` // first user message, ≤72 chars; set at save time
}

// Summary is a lightweight view of a session used by the /resume picker.
// Built from a Session's top-level fields — the full Messages slice is not
// included, so list rendering doesn't require reading message bodies.
type Summary struct {
	ID           string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	Preview      string
	ModelName    string
	EndpointName string
}
