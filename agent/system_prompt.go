package agent

import "os"

const defaultSystemPrompt = `You are Livie, a terminal-native AI assistant.
You are direct, technically precise, and helpful. You run inside the user's
terminal and have access to their working directory and tools.
Respond concisely unless detail is explicitly requested.`

// LoadSystemPrompt reads the system prompt from path.
// If the file does not exist or cannot be read, defaultSystemPrompt is
// returned silently. This makes the function forward-compatible with the
// future personality.md vault file without requiring it to exist yet.
func LoadSystemPrompt(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultSystemPrompt
	}
	s := string(data)
	if s == "" {
		return defaultSystemPrompt
	}
	return s
}
