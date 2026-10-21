package cortex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type exportMessage struct {
	UUID        string    `json:"uuid"`
	Text        string    `json:"text"`
	Sender      string    `json:"sender"`
	CreatedAt   time.Time `json:"created_at"`
	Attachments []any     `json:"attachments"`
	Files       []any     `json:"files"`
}

type exportConversation struct {
	UUID         string          `json:"uuid"`
	Name         string          `json:"name"`
	CreatedAt    time.Time       `json:"created_at"`
	ChatMessages []exportMessage `json:"chat_messages"`
}

// LoadClaudeExport reads conversations.json + design_chats/*.json from a Claude.ai
// export directory and returns them flattened into Observations for the given user.
// Idempotency keys are deterministic so re-imports insert nothing.
func LoadClaudeExport(dir, userID string) ([]Observation, int, error) {
	convs, err := loadConvFile(filepath.Join(dir, "conversations.json"))
	if err != nil {
		return nil, 0, fmt.Errorf("conversations: %w", err)
	}
	dcConvs, err := loadDesignChatsDir(filepath.Join(dir, "design_chats"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, 0, fmt.Errorf("design_chats: %w", err)
	}
	convs = append(convs, dcConvs...)
	obs := make([]Observation, 0, 8000)
	for _, c := range convs {
		for _, m := range c.ChatMessages {
			if m.Text == "" {
				continue
			}
			atStr := m.CreatedAt.UTC().Format(time.RFC3339Nano)
			id := ObservationID(userID, ObservationID(m.Sender, m.Text, atStr, c.UUID), "", "")
			obs = append(obs, Observation{
				ID:             id,
				UserID:         userID,
				Source:         "claude_export",
				Content:        m.Text,
				At:             m.CreatedAt.UTC(),
				IdempotencyKey: ObservationID(m.Sender, m.Text, atStr, c.UUID),
				Salience:       0.5,
				Refs: map[string]any{
					"sender":            m.Sender,
					"conversation_uuid": c.UUID,
					"conversation_name": c.Name,
					"attachments_count": len(m.Attachments) + len(m.Files),
				},
			})
		}
	}
	return obs, len(convs), nil
}

func loadConvFile(path string) ([]exportConversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c []exportConversation
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return c, nil
}

func loadDesignChatsDir(dir string) ([]exportConversation, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []exportConversation
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var c exportConversation
		err = json.NewDecoder(f).Decode(&c)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", e.Name(), err)
		}
		if c.UUID == "" {
			c.UUID = "design_" + e.Name()
		}
		if c.Name == "" {
			c.Name = "design_chat_" + e.Name()
		}
		out = append(out, c)
	}
	return out, nil
}
