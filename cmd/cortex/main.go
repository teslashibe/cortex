package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/teslashibe/cortex"
	"github.com/teslashibe/cortex/llm"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
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
	var options []cortex.Option
	if endpoint := os.Getenv("CORTEX_CLASSIFIER_URL"); endpoint != "" {
		classifier, err := cortex.NewHTTPClassifier(endpoint, os.Getenv("CORTEX_CLASSIFIER_MODEL"), os.Getenv("CORTEX_CLASSIFIER_KEY"))
		if err != nil {
			return err
		}
		options = append(options, cortex.WithClassifier(classifier))
	}
	if model := os.Getenv("CORTEX_EMBEDDING_MODEL"); model != "" {
		embedder, err := llm.NewEmbeddingClient(llm.Config{BaseURL: os.Getenv("CORTEX_EMBEDDING_URL"), APIKey: os.Getenv("CORTEX_EMBEDDING_KEY"), Model: model, Timeout: cortex.ModelTimeout})
		if err != nil {
			return err
		}
		options = append(options, cortex.WithEmbedder(embedder))
	}
	engine := cortex.NewEngine(store, client, options...)
	listen := os.Getenv("CORTEX_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:8090"
	}
	server := &http.Server{
		Addr: listen, Handler: cortex.NewAPI(engine).Routes(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: cortex.ConsolidationTimeout + 10*time.Second, IdleTimeout: 60 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("cortex listening on %s model=%s", listen, client.Model())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
