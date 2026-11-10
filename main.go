package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed all:front/dist
var frontendFS embed.FS

// idk if embed should be in main and passed
// or where server stuct located

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
	JWTKey := envOr("JWTKEY", "FLJjgksl9842($@$fskfFF)")
	server := &Server{store, []byte(JWTKey)}
	sub, err := fs.Sub(frontendFS, "front/dist")
	if err != nil {
		return fmt.Errorf("getting static: %w", err)
	}
	port := envOr("PORT", "8989")
	addr := "localhost:" + port
	srv := http.Server{
		Addr:         addr,
		Handler:      server.GetHandler(sub),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	fmt.Println("server started on", port)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func envOr(key, def string) string {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	return val
}
