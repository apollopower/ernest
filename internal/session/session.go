package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"ernest/internal/provider"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const currentVersion = 1

// Session represents a serializable conversation.
type Session struct {
	Version    int                `json:"version"`
	ID         string             `json:"id"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	ProjectDir string             `json:"project_dir"`
	Summary    string             `json:"summary"`
	Messages   []provider.Message `json:"messages"`
	TokenCount int                `json:"token_count"`
}

// sessionMeta is a lightweight struct for listing sessions without loading
// the full Messages array. Go's encoding/json still scans the full file but
// avoids allocating the Messages slice.
type sessionMeta struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	ProjectDir string    `json:"project_dir"`
	Summary    string    `json:"summary"`
	TokenCount int       `json:"token_count"`
}

// SessionInfo is the public metadata returned by ListSessions.
type SessionInfo struct {
	ID         string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ProjectDir string
	Summary    string
	TokenCount int
}

// New creates a new session with a generated ID.
func New(projectDir string) *Session {
	return &Session{
		Version:    currentVersion,
		ID:         generateID(),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		ProjectDir: projectDir,
	}
}

// Save writes the session to disk at SessionDir()/{id}.json.
func (s *Session) Save() error {
	dir := SessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create session directory: %w", err)
	}

	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal session: %w", err)
	}

	path := filepath.Join(dir, s.ID+".json")
	return os.WriteFile(path, data, 0o600)
}

// SetMessages updates the session's messages and summary.
func (s *Session) SetMessages(msgs []provider.Message) {
	s.Messages = msgs
	// Set summary from first user message if not already set by compaction
	if s.Summary == "" && len(msgs) > 0 {
		for _, msg := range msgs {
			if msg.Role == provider.RoleUser {
				for _, block := range msg.Content {
					if block.Type == "text" && block.Text != "" {
						summary := block.Text
						if len(summary) > 100 {
							summary = summary[:100] + "..."
						}
						s.Summary = summary
						return
					}
				}
			}
		}
	}
}

// Load reads a session from a JSON file.
func Load(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read session file: %w", err)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("cannot parse session file: %w", err)
	}

	if s.Version < 1 || s.Version > currentVersion {
		return nil, fmt.Errorf("session version %d not supported (expected 1-%d)", s.Version, currentVersion)
	}

	return &s, nil
}

// ListSessions returns metadata for all sessions, sorted by UpdatedAt descending.
func ListSessions() ([]SessionInfo, error) {
	dir := SessionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read session directory: %w", err)
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // skip unreadable files
		}

		var meta sessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue // skip corrupt files
		}

		sessions = append(sessions, SessionInfo{
			ID:         meta.ID,
			CreatedAt:  meta.CreatedAt,
			UpdatedAt:  meta.UpdatedAt,
			ProjectDir: meta.ProjectDir,
			Summary:    meta.Summary,
			TokenCount: meta.TokenCount,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// SessionDir returns the directory where sessions are stored.
func SessionDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	return filepath.Join(configDir, "ernest", "sessions")
}

func generateID() string {
	b := make([]byte, 4) // 4 bytes = 8 hex chars
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return hex.EncodeToString(b)
}
