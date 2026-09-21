// bob is the API: projects, sessions, turns, and the stored record of every event.
//
//	BOB_DATABASE_URL   postgres connection string (required)
//	BOB_PROJECTS_FILE  the projects Bob serves, YAML (required). Each also has a container
//	                   declared in the same deploy's compose file; Bob never starts one.
//	BOB_ADDR           listen address (default :8090)
//	BOB_RUNTIME_KEY    derives each project's runtime password, and must match what the compose
//	                   file passes that container as BOB_RUNTIME_TOKEN (required)
//	BOB_RUNTIME_HOSTS  local development only: project=host:port,… when Bob runs on the host and
//	                   cannot resolve container names
//	GOOGLE_CLIENT_ID   Google sign-in (required)
//	BOB_SESSION_SECRET signs session cookies (required)
//	BOB_PROJECT_MAP    who may sign in and which projects they use (required), JSON:
//	                   {"kai@example.com": ["*"], "tester@example.com": ["wolf"]}; "*" = admin
//	BOB_PROJECT_MAP_FILE the same map read from a file (BOB_PROJECT_MAP wins)
//	BOB_ALLOWED_EMAILS deprecated: comma-separated emails, each an admin, when no map is set
//	BOB_WEB_DIR        serve the built web app from here (optional; dev uses Vite)
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/projects"
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

	projectsFile := os.Getenv("BOB_PROJECTS_FILE")
	if projectsFile == "" {
		log.Fatal("BOB_PROJECTS_FILE is required: the projects Bob serves")
	}
	list, err := projects.Load(projectsFile)
	if err != nil {
		log.Fatal(err)
	}
	if err := st.ReconcileProjects(ctx, list); err != nil {
		log.Fatalf("reconciling %s: %v", projectsFile, err)
	}
	if absent, err := st.AbsentProjects(ctx); err != nil {
		log.Fatal(err)
	} else if len(absent) > 0 {
		log.Printf("not serving projects missing from %s (their chats and schedules are kept, and paused): %s",
			projectsFile, strings.Join(absent, ", "))
	}

	people, deprecated, err := access.Load(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if deprecated {
		log.Print("BOB_ALLOWED_EMAILS is deprecated: every listed email is an admin. Set BOB_PROJECT_MAP instead.")
	}
	signIn := &auth.Auth{ClientID: os.Getenv("GOOGLE_CLIENT_ID"), Secret: []byte(os.Getenv("BOB_SESSION_SECRET")), Allowed: people.Allowed}
	if signIn.ClientID == "" || len(signIn.Secret) < 16 {
		log.Fatal("GOOGLE_CLIENT_ID and BOB_SESSION_SECRET (16+ chars) are required")
	}

	runtimeKey := []byte(os.Getenv("BOB_RUNTIME_KEY"))
	if len(runtimeKey) < 16 {
		log.Fatal("BOB_RUNTIME_KEY (16+ chars) is required: it derives each project container's password")
	}
	hosts, err := parseHosts(os.Getenv("BOB_RUNTIME_HOSTS"))
	if err != nil {
		log.Fatalf("BOB_RUNTIME_HOSTS: %v", err)
	}

	app := &app{
		auth:    signIn,
		access:  people,
		webDir:  os.Getenv("BOB_WEB_DIR"),
		store:   st,
		broker:  broker.New(),
		runtime: runtime.NewManager(runtime.Config{Hosts: hosts, TokenKey: runtimeKey}),
		turns:   map[string]context.CancelFunc{},
		now:     time.Now,
	}
	app.projectOf = app.storeProjectOf
	go app.scheduleLoop(ctx)

	srv := &http.Server{Addr: env("BOB_ADDR", ":8090"), Handler: app.routes()}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("bob listening on %s, serving %d projects from %s", srv.Addr, len(list), projectsFile)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// parseHosts reads "project=host:port,other=host:port" (BOB_RUNTIME_HOSTS).
func parseHosts(s string) (map[string]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		project, host, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || project == "" || host == "" {
			return nil, fmt.Errorf("want project=host:port, got %q", pair)
		}
		out[project] = host
	}
	return out, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
