// bob is the API: projects, sessions, turns, and the stored record of every event.
//
//	BOB_DATABASE_URL   postgres connection string (required)
//	BOB_ADDR           listen address (default :8090)
//	BOB_DOCKER_SOCKET  default /var/run/docker.sock
//	BOB_RUNTIME_IMAGE  image for projects that name none (default bob-runtime:dev)
//	BOB_DOCKER_NETWORK run project containers on this network (set when Bob itself is containerised)
//	GOOGLE_CLIENT_ID   Google sign-in (required)
//	BOB_SESSION_SECRET signs session cookies (required)
//	BOB_ALLOWED_EMAILS comma-separated Google accounts allowed to sign in (required)
//	BOB_WEB_DIR        serve the built web app from here (optional; dev uses Vite)
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

	"github.com/badcodetv/bob/internal/auth"
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

	allowed := map[string]bool{}
	for _, e := range strings.Split(os.Getenv("BOB_ALLOWED_EMAILS"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			allowed[e] = true
		}
	}
	signIn := &auth.Auth{ClientID: os.Getenv("GOOGLE_CLIENT_ID"), Secret: []byte(os.Getenv("BOB_SESSION_SECRET")), Allowed: allowed}
	if signIn.ClientID == "" || len(signIn.Secret) < 16 || len(allowed) == 0 {
		log.Fatal("GOOGLE_CLIENT_ID, BOB_SESSION_SECRET (16+ chars) and BOB_ALLOWED_EMAILS are required")
	}

	pass := map[string]string{}
	for _, k := range strings.Split(os.Getenv("BOB_PASS_ENV"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			if v, ok := os.LookupEnv(k); ok {
				pass[k] = v
			}
		}
	}
	app := &app{
		auth:   signIn,
		webDir: os.Getenv("BOB_WEB_DIR"),
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
