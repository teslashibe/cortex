package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/teslashibe/cortex"
)

type batchReq struct {
	UserID       string                    `json:"user_id"`
	Observations []cortex.ObservationInput `json:"observations"`
}

type batchResp struct {
	Received int `json:"received"`
	Inserted int `json:"inserted"`
}

func main() {
	dir := flag.String("dir", "", "claude-export directory containing conversations.json and design_chats/")
	user := flag.String("user", "default", "user id")
	endpoint := flag.String("endpoint", "http://localhost:8090", "cortex base URL")
	chunk := flag.Int("chunk", 500, "observations per HTTP request")
	flag.Parse()
	if *dir == "" {
		log.Fatal("--dir required")
	}

	obs, convs, err := cortex.LoadClaudeExport(*dir, *user)
	if err != nil {
		log.Fatalf("load: %v", err)
	}
	log.Printf("import: %d conversations → %d observations", convs, len(obs))

	payloads := make([]cortex.ObservationInput, 0, len(obs))
	for _, o := range obs {
		payloads = append(payloads, cortex.ObservationInput{
			Sender:           strOf(o.Refs["sender"]),
			Source:           o.Source,
			Content:          o.Content,
			At:               o.At,
			ConversationUUID: strOf(o.Refs["conversation_uuid"]),
			ConversationName: strOf(o.Refs["conversation_name"]),
			Refs:             o.Refs,
		})
	}

	ctx := context.Background()
	totalReceived, totalInserted := 0, 0
	for i := 0; i < len(payloads); i += *chunk {
		end := i + *chunk
		if end > len(payloads) {
			end = len(payloads)
		}
		recv, ins, err := postBatch(ctx, *endpoint, *user, payloads[i:end])
		if err != nil {
			log.Fatalf("post chunk [%d:%d]: %v", i, end, err)
		}
		totalReceived += recv
		totalInserted += ins
		log.Printf("import: chunk [%d:%d] received=%d inserted=%d", i, end, recv, ins)
	}
	log.Printf("import: done received=%d inserted=%d (rerun is idempotent)", totalReceived, totalInserted)
}

func postBatch(ctx context.Context, endpoint, userID string, obs []cortex.ObservationInput) (int, int, error) {
	body, err := json.Marshal(batchReq{UserID: userID, Observations: obs})
	if err != nil {
		return 0, 0, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/observations", bytes.NewReader(body))
	if err != nil {
		return 0, 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("http %d: %s", resp.StatusCode, raw)
	}
	var r batchResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, 0, fmt.Errorf("decode: %w", err)
	}
	return r.Received, r.Inserted, nil
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
