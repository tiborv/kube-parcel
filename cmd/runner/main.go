package main

import (
	"context"
	_ "embed"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tiborv/kube-parcel/pkg/config"
	"github.com/tiborv/kube-parcel/pkg/runner"
)

//go:embed ui/index.html
var indexHTML string

func main() {
	log.Printf("🚀 kube-parcel runner v%s starting...", config.Version)
	log.Printf("PID: %d", os.Getpid())

	srv := runner.NewServer()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(indexHTML))
	})

	mux.HandleFunc("/parcel/upload", srv.HandleUpload)
	mux.HandleFunc("/parcel/status", srv.HandleStatus)
	mux.HandleFunc("/ws/logs", srv.HandleWebSocket)

	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	go func() {
		log.Println("🌐 HTTP server listening on :8080")
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigChan
	log.Printf("Received signal: %s, initiating shutdown...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}

	log.Println("👋 Shutdown complete")
}
