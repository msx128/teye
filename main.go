package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Println(err)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() { <-ctx.Done(); cancel() }()
	store, err := NewStore(ctx, "./data.db")
	if err != nil {
		return fmt.Errorf("creating storage: %w", err)
	}
	fmt.Println(store)
	return nil
}
