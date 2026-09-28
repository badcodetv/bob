// bob is the API: projects, sessions, turns, and the stored record of every event.
//
//	BOB_DATABASE_URL   postgres connection string (required)
//	BOB_PROJECTS       the projects Bob serves, comma-separated: enc,marketing,wolf (required).
//	                   Each also has a container declared in the same deploy's compose file;
//	                   Bob never starts one.
//	BOB_RUNTIME_TOKEN_<NAME>  one per project (required): the password of its runtime server, the
//	                   same value the compose file passes that container as BOB_RUNTIME_TOKEN.
//	                   <NAME> is the project's name upper-cased, - as _. 32+ characters
//	                   (openssl rand -hex 32).
//	BOB_PUBLIC_URL     where people reach Bob, no trailing slash (required), e.g.
//	                   https://bob.box.badcode.tv; for links to chats
//	BOB_ADDR           listen address (default :8070)
//	BOB_RUNTIME_HOSTS  local development only: project=host:port,… when Bob runs on the host and
//	                   cannot resolve container names
//	BOB_DRIVE_CLIENT_ID, BOB_DRIVE_CLIENT_SECRET  the OAuth client scripts/drive-token used to mint
//	                   Drive tokens (required if any BOB_DRIVE_TOKEN_<NAME> is set)
//	BOB_DRIVE_TOKEN_<NAME>  optional, one per project: that project's Drive refresh token
//	                   (scripts/drive-token). <NAME> as BOB_RUNTIME_TOKEN_<NAME> above. A project
//	                   with none set has no Drive client and no drive_* MCP tools.
//	GOOGLE_CLIENT_ID   Google sign-in (required)
//	BOB_SESSION_SECRET signs session cookies and each chat's MCP token (required)
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
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/badcodetv/bob/internal/access"
	"github.com/badcodetv/bob/internal/auth"
	"github.com/badcodetv/bob/internal/broker"
	"github.com/badcodetv/bob/internal/drive"
	"github.com/badcodetv/bob/internal/mcp"
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

	names, tokens, err := projectsFromEnv(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	public, err := publicURL(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if err := st.ReconcileProjects(ctx, names); err != nil {
		log.Fatalf("reconciling BOB_PROJECTS: %v", err)
	}
	if absent, err := st.AbsentProjects(ctx); err != nil {
		log.Fatal(err)
	} else if len(absent) > 0 {
		log.Printf("not serving projects missing from BOB_PROJECTS (their chats and schedules are kept, and paused): %s",
			strings.Join(absent, ", "))
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

	hosts, err := parseHosts(os.Getenv("BOB_RUNTIME_HOSTS"))
	if err != nil {
		log.Fatalf("BOB_RUNTIME_HOSTS: %v", err)
	}

	driveClients, err := driveClientsFromEnv(ctx, os.Getenv, names)
	if err != nil {
		log.Fatal(err)
	}

	app := &app{
		auth:      signIn,
		access:    people,
		webDir:    os.Getenv("BOB_WEB_DIR"),
		publicURL: public,
		store:     st,
		broker:    broker.New(),
		runtime:   runtime.NewManager(runtime.Config{Hosts: hosts, Tokens: tokens}),
		drive:     driveClients,
		mcp:       mcp.New(),
		turns:     map[string]context.CancelFunc{},
		now:       time.Now,
	}
	app.mcp.Register(bobWhoami)
	app.mcp.Register(app.workerTools()...)
	app.projectOf = app.storeProjectOf
	go app.scheduleLoop(ctx)

	srv := &http.Server{Addr: env("BOB_ADDR", ":8070"), Handler: app.routes()}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("bob listening on %s, serving %s", srv.Addr, strings.Join(names, ", "))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// projectName is the shape the projects table enforces. A project's name is also its container
// name and its volume name, so it can never change.
var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// projectsFromEnv reads BOB_PROJECTS and each listed project's runtime token. It fails rather than
// guessing, naming the variable to fix: a bad name, a duplicate, or a missing or short token stops
// Bob at boot, where it is obvious, instead of at the first turn.
func projectsFromEnv(getenv func(string) string) (names []string, tokens map[string]string, err error) {
	tokens = map[string]string{}
	for _, name := range strings.Split(getenv("BOB_PROJECTS"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch variable := runtime.TokenVar(name); {
		case !projectName.MatchString(name):
			return nil, nil, fmt.Errorf("BOB_PROJECTS: project name %q must match %s", name, projectName)
		case tokens[name] != "":
			return nil, nil, fmt.Errorf("BOB_PROJECTS: project %q is listed twice", name)
		case getenv(variable) == "":
			return nil, nil, fmt.Errorf("%s is required: project %s's runtime token (openssl rand -hex 32), the same value its container gets as BOB_RUNTIME_TOKEN", variable, name)
		case len(getenv(variable)) < 32:
			return nil, nil, fmt.Errorf("%s is too short: 32+ characters (openssl rand -hex 32)", variable)
		default:
			names, tokens[name] = append(names, name), getenv(variable)
		}
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("BOB_PROJECTS is required: the projects Bob serves, comma-separated (e.g. enc,marketing)")
	}
	return names, tokens, nil
}

// driveClientsFromEnv builds a Drive client for each project that has a BOB_DRIVE_TOKEN_<NAME>
// set. Most projects have none, and Bob starts exactly as before for them. A project that does
// have one but is missing BOB_DRIVE_CLIENT_ID/BOB_DRIVE_CLIENT_SECRET (the OAuth client that
// minted it) fails at boot, not on the first drive_* tool call.
func driveClientsFromEnv(ctx context.Context, getenv func(string) string, projects []string) (map[string]*drive.Client, error) {
	clientID, clientSecret := getenv("BOB_DRIVE_CLIENT_ID"), getenv("BOB_DRIVE_CLIENT_SECRET")
	clients := map[string]*drive.Client{}
	for _, name := range projects {
		token := getenv(drive.TokenVar(name))
		if token == "" {
			continue
		}
		if clientID == "" || clientSecret == "" {
			return nil, fmt.Errorf("%s is set for project %s: BOB_DRIVE_CLIENT_ID and BOB_DRIVE_CLIENT_SECRET are also required (the OAuth client scripts/drive-token used)", drive.TokenVar(name), name)
		}
		c, err := drive.New(ctx, clientID, clientSecret, token)
		if err != nil {
			return nil, fmt.Errorf("project %s: building Drive client: %w", name, err)
		}
		clients[name] = c
	}
	return clients, nil
}

// publicURL reads BOB_PUBLIC_URL, where people reach Bob. Links to chats are built on it by
// appending a path, so it must not end in a slash.
func publicURL(getenv func(string) string) (string, error) {
	u := getenv("BOB_PUBLIC_URL")
	switch {
	case u == "":
		return "", fmt.Errorf("BOB_PUBLIC_URL is required: where people reach Bob, e.g. https://bob.box.badcode.tv")
	case !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://"):
		return "", fmt.Errorf("BOB_PUBLIC_URL must start with http:// or https://, got %q", u)
	case strings.HasSuffix(u, "/"):
		return "", fmt.Errorf("BOB_PUBLIC_URL must not end in /, got %q", u)
	}
	return u, nil
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
