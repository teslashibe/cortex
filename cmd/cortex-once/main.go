package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/teslashibe/cortex"
	"github.com/teslashibe/cortex/llm"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dir := flag.String("dir", "", "optional claude-export directory to import before consolidation")
	user := flag.String("user", "default", "user id")
	tier := flag.String("tier", "internal", "profile tier (public|internal)")
	chunk := flag.Int("chunk", cortex.DefaultChunkSize, "maximum observations per synchronous call (1-200)")
	byDay := flag.Bool("by-day", false, "keep observations from different UTC days in separate chunks")
	flag.Parse()
	if *tier != "public" && *tier != "internal" {
		return errors.New("tier must be public|internal")
	}
	if *chunk <= 0 || *chunk > cortex.DefaultChunkSize {
		return fmt.Errorf("chunk must be 1-%d", cortex.DefaultChunkSize)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL required")
	}
	client, err := llm.New(llm.Config{BaseURL: os.Getenv("LLM_BASE_URL"), APIKey: os.Getenv("LLM_API_KEY"), Model: os.Getenv("CORTEX_MODEL"), Timeout: cortex.ModelTimeout})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := cortex.NewStore(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ApplyMigrations(ctx, cortex.Migrations()); err != nil {
		return err
	}
	engine := cortex.NewEngine(store, client)
	if *dir != "" {
		obs, _, err := cortex.LoadClaudeExport(*dir, *user)
		if err != nil {
			return err
		}
		inserted, err := engine.Observe(ctx, obs)
		if err != nil {
			return err
		}
		log.Printf("imported=%d received=%d", inserted, len(obs))
	}
	result, err := engine.Consolidate(ctx, *user, cortex.ConsolidationOptions{ChunkSize: *chunk, ByDay: *byDay})
	log.Printf("consolidation chunks=%d observations=%d created=%d reinforced=%d tokens=%d", len(result.Runs), result.Counts.ObservationsProcessed, result.Counts.MemoriesCreated, result.Counts.MemoriesReinforced, result.Counts.TokensUsed)
	if err != nil && !errors.Is(err, cortex.ErrNoObservations) {
		return err
	}
	profile, err := engine.Profile(ctx, *user, *tier)
	if err != nil {
		return err
	}
	fmt.Print(profile)
	return nil
}
