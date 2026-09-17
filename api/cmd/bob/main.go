// bob is the API: projects, sessions, turns, and the stored record of every event.
//
//	BOB_DATABASE_URL   postgres connection string (required)
//	BOB_ADDR           listen address (default :8090)
//	BOB_DOCKER_SOCKET  default /var/run/docker.sock
//	BOB_RUNTIME_IMAGE  image for projects that name none (default bob-runtime:dev)
//	BOB_DOCKER_NETWORK run project containers on this network (set when Bob itself is containerised)
//	BOB_PASS_ENV       comma-separated variables copied into project containers,
//	                   e.g. CLAUDE_CODE_OAUTH_TOKEN,ANTHROPIC_API_KEY,GITHUB_TOKEN
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/docker"
	"github.com/badcodetv/bob/internal/runtime"
	"github.com/badcodetv/bob/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("BOB_DATABASE_URL")
	if dbURL == "" {
		log.Fatal("BOB_DATABASE_URL is required")
	}
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	pass := map[string]string{}
	for _, k := range strings.Split(os.Getenv("BOB_PASS_ENV"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			if v, ok := os.LookupEnv(k); ok {
				pass[k] = v
			}
		}
	}
	app := &app{
		store:  st,
		broker: broker.New(),
		runtime: runtime.NewManager(docker.New(env("BOB_DOCKER_SOCKET", "/var/run/docker.sock")), runtime.Config{
			DefaultImage: env("BOB_RUNTIME_IMAGE", "bob-runtime:dev"),
			Network:      os.Getenv("BOB_DOCKER_NETWORK"),
			PassEnv:      pass,
		}),
		turns: map[string]context.CancelFunc{},
	}

	srv := &http.Server{Addr: env("BOB_ADDR", ":8090"), Handler: app.routes()}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("bob listening on %s (passing %d env vars to projects)", srv.Addr, len(pass))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
