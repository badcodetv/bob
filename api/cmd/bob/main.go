// bob is the API: projects, sessions, turns, and the stored record of every event.
//
//	BOB_DATABASE_URL   postgres connection string (required)
//	BOB_ADDR           listen address (default :8090)
//	BOB_DOCKER_SOCKET  default /var/run/docker.sock
//	BOB_RUNTIME_IMAGE  image for projects that name none (default bob-runtime:dev)
//	BOB_DOCKER_NETWORK run project containers on this network (set when Bob itself is containerised)
//	GOOGLE_CLIENT_ID   Google sign-in (required)
//	BOB_SESSION_SECRET signs session cookies (required)
//	BOB_PROJECT_MAP    who may sign in and which projects they use (required), JSON:
//	                   {"kai@example.com": ["*"], "tester@example.com": ["wolf"]}; "*" = admin
//	BOB_PROJECT_MAP_FILE the same map read from a file (BOB_PROJECT_MAP wins)
//	BOB_ALLOWED_EMAILS deprecated: comma-separated emails, each an admin, when no map is set
//	BOB_WEB_DIR        serve the built web app from here (optional; dev uses Vite)
//	BOB_SECRETS_KEY    encrypts project secrets: 32 bytes, base64 (openssl rand -base64 32);
//	                   unset = project secrets are off
//	BOB_PASS_ENV       comma-separated variables copied into project containers,
//	                   e.g. CLAUDE_CODE_OAUTH_TOKEN,ANTHROPIC_API_KEY,GITHUB_TOKEN
//	BOB_PROJECT_MEMORY memory limit per project container, e.g. 8g (default: none)
//	BOB_PROJECT_CPUS   CPU limit per project container, e.g. 4 (default: none)
//	BOB_PROJECT_PIDS   process limit per project container (default 4096)
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/docker"
	"github.com/badcodetv/bob/internal/runtime"
	"github.com/badcodetv/bob/internal/secrets"
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

	var box *secrets.Box
	if key := os.Getenv("BOB_SECRETS_KEY"); key != "" {
		if box, err = secrets.NewBox(key); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Print("BOB_SECRETS_KEY is not set: project secrets are off")
	}

	pass := map[string]string{}
	for _, k := range strings.Split(os.Getenv("BOB_PASS_ENV"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			if v, ok := os.LookupEnv(k); ok {
				pass[k] = v
			}
		}
	}
	memory, err := parseBytes(os.Getenv("BOB_PROJECT_MEMORY"))
	if err != nil {
		log.Fatalf("BOB_PROJECT_MEMORY: %v", err)
	}
	cpus, err := strconv.ParseFloat(env("BOB_PROJECT_CPUS", "0"), 64)
	if err != nil || cpus < 0 {
		log.Fatalf("BOB_PROJECT_CPUS: not a number of CPUs: %q", os.Getenv("BOB_PROJECT_CPUS"))
	}
	pids, err := strconv.ParseInt(env("BOB_PROJECT_PIDS", "4096"), 10, 64)
	if err != nil || pids < 0 {
		log.Fatalf("BOB_PROJECT_PIDS: not a number: %q", os.Getenv("BOB_PROJECT_PIDS"))
	}

	app := &app{
		auth:    signIn,
		access:  people,
		secrets: box,
		webDir:  os.Getenv("BOB_WEB_DIR"),
		store:   st,
		broker:  broker.New(),
		runtime: runtime.NewManager(docker.New(env("BOB_DOCKER_SOCKET", "/var/run/docker.sock")), runtime.Config{
			DefaultImage: env("BOB_RUNTIME_IMAGE", "bob-runtime:dev"),
			Network:      os.Getenv("BOB_DOCKER_NETWORK"),
			PassEnv:      pass,
			TokenKey:     signIn.Secret,
			Memory:       memory,
			NanoCPUs:     int64(cpus * 1e9),
			PidsLimit:    pids,
		}),
		turns: map[string]context.CancelFunc{},
		now:   time.Now,
	}
	app.projectOf = app.storeProjectOf
	app.runtime.(*runtime.Manager).SetSecrets(app.projectSecrets)
	// Before any turn can run: containers made with an older image, token or limits are replaced
	// on their next use.
	if projects, err := st.Projects(ctx); err != nil {
		log.Fatal(err)
	} else if replaced, err := app.runtime.(*runtime.Manager).ReplaceStale(ctx, projects); err != nil {
		log.Printf("checking project containers: %v", err)
	} else if len(replaced) > 0 {
		log.Printf("replaced project containers with changed settings: %s", strings.Join(replaced, ", "))
	}
	go app.scheduleLoop(ctx)

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

// parseBytes reads a size such as 512m or 8g ("" is 0, no limit).
func parseBytes(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k':
		mult = 1 << 10
	case 'm':
		mult = 1 << 20
	case 'g':
		mult = 1 << 30
	}
	if mult > 1 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not a size like 512m or 8g: %q", s)
	}
	return n * mult, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
