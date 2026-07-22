package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed static/*
var staticFS embed.FS

func serve(ctx context.Context, webAddr string, hub *Hub, logPath string) error {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle(RouteIndex, http.FileServer(http.FS(static)))
	mux.HandleFunc(RouteEvents, streamSamples(hub))
	mux.HandleFunc(RouteLog, serveLog(logPath))
	mux.HandleFunc(RouteHistory, serveHistory(logPath))

	srv := &http.Server{Addr: webAddr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	shown := webAddr
	if strings.HasPrefix(shown, ":") {
		shown = "localhost" + shown
	}
	log.Printf("%s — dashboard live at http://%s (Ctrl+C to stop)", AppName, shown)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func streamSamples(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch, snapshot, unsub := hub.Subscribe()
		defer unsub()

		if len(snapshot) > 0 {
			payload, err := json.Marshal(snapshot)
			if err == nil {
				fmt.Fprintf(w, "event: backfill\ndata: %s\n\n", payload)
				flusher.Flush()
			}
		}

		for {
			select {
			case <-r.Context().Done():
				return
			case s, ok := <-ch:
				if !ok {
					return
				}
				payload, err := json.Marshal(s)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}

func serveLog(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")

		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(w, "# no telemetry recorded yet")
				return
			}
			http.Error(w, "log unavailable: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		_, _ = io.Copy(w, f)
	}
}
