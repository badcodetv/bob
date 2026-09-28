package store

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/bob/internal/embed"
	"github.com/badcodetv/bob/internal/labels"
	"github.com/jackc/pgx/v5"
)

// memStore is a test store with projects wolf and enc, and a clock the test moves by hand.
func memStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	st := newTestStore(t)
	if err := st.ReconcileProjects(t.Context(), []string{"wolf", "enc"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }
	return st, &now
}

// remember writes a memory embedded with embed.Fake, one minute after the last.
func remember(t *testing.T, st *Store, now *time.Time, m Memory) Memory {
	t.Helper()
	*now = now.Add(time.Minute)
	if m.Project == "" {
		m.Project = "wolf"
	}
	vec, err := embed.Fake{}.Embed(t.Context(), m.Content)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.CreateMemory(t.Context(), m, vec, "")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func search(t *testing.T, st *Store, q MemorySearch) []MemoryHit {
	t.Helper()
	if q.Project == "" {
		q.Project = "wolf"
	}
	if q.Query != "" && q.QueryEmbedding == nil {
		q.QueryEmbedding, _ = embed.Fake{}.Embed(t.Context(), q.Query)
	}
	hits, err := st.SearchMemories(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func hitIDs(hits []MemoryHit) []string {
	ids := []string{}
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return ids
}

// A memory is stored as written and read back whole; another project cannot see it.
func TestCreateAndReadMemory(t *testing.T) {
	st, now := memStore(t)
	ctx := t.Context()
	sess, err := st.CreateSession(ctx, "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	m := remember(t, st, now, Memory{Content: "CPI came in hot", Labels: map[string]string{"kind": "note"},
		CreatedByWorker: "scout", CreatedBySession: sess.ID})
	if m.ID == "" || m.Project != "wolf" || m.Content != "CPI came in hot" || m.Labels["kind"] != "note" ||
		m.CreatedByWorker != "scout" || m.CreatedBySession != sess.ID || !m.CreatedAt.Equal(*now) {
		t.Errorf("created = %+v", m)
	}
	got, err := st.Memory(ctx, "wolf", m.ID)
	if err != nil || got.Content != m.Content || got.Labels["kind"] != "note" {
		t.Errorf("Memory = %+v, %v", got, err)
	}
	if _, err := st.Memory(ctx, "enc", m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another project's memory: err = %v, want ErrNotFound", err)
	}
	if hits := search(t, st, MemorySearch{Project: "enc"}); len(hits) != 0 {
		t.Errorf("enc sees %v", hits)
	}
	plain := remember(t, st, now, Memory{Content: "no labels"})
	if plain.Labels == nil || plain.CreatedBySession != "" {
		t.Errorf("plain memory = %+v; want labels {} and no session", plain)
	}
	// A deleted session leaves its memories, unattributed to it.
	if err := st.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Memory(ctx, "wolf", m.ID); got.CreatedBySession != "" {
		t.Errorf("after the session went, created_by_session = %q", got.CreatedBySession)
	}
}

func TestCreateMemoryRejects(t *testing.T) {
	st, _ := memStore(t)
	vec, _ := embed.Fake{}.Embed(t.Context(), "x")
	for name, tc := range map[string]struct {
		m   Memory
		vec []float32
	}{
		"blank content":    {Memory{Project: "wolf", Content: "  "}, vec},
		"over 24 KiB":      {Memory{Project: "wolf", Content: strings.Repeat("a", MaxMemoryBytes+1)}, vec},
		"bad label":        {Memory{Project: "wolf", Content: "x", Labels: map[string]string{"bad key": "v"}}, vec},
		"reserved label":   {Memory{Project: "wolf", Content: "x", Labels: map[string]string{"bob.kind": "v"}}, vec},
		"no embedding":     {Memory{Project: "wolf", Content: "x"}, nil},
		"narrow embedding": {Memory{Project: "wolf", Content: "x"}, []float32{1, 2, 3}},
	} {
		if _, err := st.CreateMemory(t.Context(), tc.m, tc.vec, ""); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := st.CreateMemory(t.Context(), Memory{Project: "wolf", Content: strings.Repeat("a", MaxMemoryBytes)}, vec, ""); err != nil {
		t.Errorf("exactly 24 KiB: %v", err)
	}
}

// Acceptance: a memory matching the query only by keyword and one matching only by vector both
// come back, fused by Reciprocal Rank Fusion; one matching by both ranks first.
func TestSearchFusesKeywordAndVector(t *testing.T) {
	st, now := memStore(t)
	// "ferries" stems to the query's "ferri" but shares no word with it, so Fake puts it far.
	kwOnly := remember(t, st, now, Memory{Content: "Ferries run hourly"})
	// "the" is a stop word, so no keyword match; Fake still puts it near "the ferry".
	vecOnly := remember(t, st, now, Memory{Content: "the the the zebra"})
	both := remember(t, st, now, Memory{Content: "the ferry is late"})

	hits := search(t, st, MemorySearch{Query: "the ferry"})
	byID := map[string]MemoryHit{}
	for _, h := range hits {
		byID[h.ID] = h
	}
	if len(hits) == 0 || hits[0].ID != both.ID {
		t.Fatalf("hits = %v; want %s (both legs) first", hitIDs(hits), both.ID)
	}
	for name, id := range map[string]string{"keyword only": kwOnly.ID, "vector only": vecOnly.ID} {
		h, ok := byID[id]
		if !ok {
			t.Errorf("%s memory missing from %v", name, hitIDs(hits))
		} else if h.Score <= 0 || h.Score >= hits[0].Score {
			t.Errorf("%s score = %v, want between 0 and %v", name, h.Score, hits[0].Score)
		}
	}
	if top := hits[0].Score; top <= 1.0/61 || top > 2.0/61+1e-9 {
		t.Errorf("top score = %v, want 1/61 < score ≤ 2/61", top)
	}

	// Without a query embedding the vector leg is skipped: keyword matches only.
	kw, err := st.SearchMemories(t.Context(), MemorySearch{Project: "wolf", Query: "the ferry"})
	if err != nil || len(kw) != 2 {
		t.Errorf("keyword-only search = %v, %v; want the two keyword matches", hitIDs(kw), err)
	}
}

// With no query, search is the filtered set newest first; a snippet is the first 500 characters.
func TestSearchNewestFirstAndFilters(t *testing.T) {
	st, now := memStore(t)
	sel := func(s string) labels.Selector {
		x, err := labels.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	a := remember(t, st, now, Memory{Content: strings.Repeat("x", 600), Labels: map[string]string{"kind": "note"}, CreatedByWorker: "scout"})
	b := remember(t, st, now, Memory{Content: "second", Labels: map[string]string{"kind": "fact"}})
	c := remember(t, st, now, Memory{Content: "third", Labels: map[string]string{"kind": "note"}, CreatedByWorker: "scout"})
	remember(t, st, now, Memory{Project: "enc", Content: "elsewhere", Labels: map[string]string{"kind": "note"}})

	hits := search(t, st, MemorySearch{})
	if fmt.Sprint(hitIDs(hits)) != fmt.Sprint([]string{c.ID, b.ID, a.ID}) {
		t.Errorf("newest first = %v", hitIDs(hits))
	}
	if len(hits[2].Snippet) != 500 || hits[2].Score != 0 || hits[2].CreatedByWorker != "scout" {
		t.Errorf("hit = snippet %d chars, score %v, worker %q", len(hits[2].Snippet), hits[2].Score, hits[2].CreatedByWorker)
	}
	for name, tc := range map[string]struct {
		q    MemorySearch
		want []string
	}{
		"selector":          {MemorySearch{Selector: sel("kind=note")}, []string{c.ID, a.ID}},
		"selector negated":  {MemorySearch{Selector: sel("kind!=note")}, []string{b.ID}},
		"created by worker": {MemorySearch{CreatedByWorker: "scout"}, []string{c.ID, a.ID}},
		"since":             {MemorySearch{Since: b.CreatedAt}, []string{c.ID, b.ID}},
		"until":             {MemorySearch{Until: b.CreatedAt}, []string{b.ID, a.ID}},
		"limit":             {MemorySearch{Limit: 1}, []string{c.ID}},
		"query and filter":  {MemorySearch{Query: "third second", Selector: sel("kind=fact")}, []string{b.ID}},
	} {
		if got := hitIDs(search(t, st, tc.q)); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	if _, err := st.SearchMemories(t.Context(), MemorySearch{Project: "wolf", Since: c.CreatedAt, Until: a.CreatedAt}); err == nil {
		t.Error("since after until should be an error")
	}
	if _, err := st.SearchMemories(t.Context(), MemorySearch{Project: "wolf", LatestPer: "bad key"}); err == nil {
		t.Error("a bad latest_per key should be an error")
	}
}

// Acceptance: a memory labelled retracts=<id> hides that memory from search and from
// CurrentMemory; reading it by id still works.
func TestRetractionHides(t *testing.T) {
	st, now := memStore(t)
	ctx := t.Context()
	old := remember(t, st, now, Memory{Content: "rates will rise", Labels: map[string]string{"name": "view"}})
	retraction := remember(t, st, now, Memory{Content: "wrong: misread the minutes", Labels: map[string]string{"retracts": old.ID}})
	// A retraction in another project does not reach across.
	other := remember(t, st, now, Memory{Content: "kept", Labels: map[string]string{"name": "kept"}})
	remember(t, st, now, Memory{Project: "enc", Content: "sneaky", Labels: map[string]string{"retracts": other.ID}})

	if got := hitIDs(search(t, st, MemorySearch{})); fmt.Sprint(got) != fmt.Sprint([]string{other.ID, retraction.ID}) {
		t.Errorf("newest = %v; want the retracted one hidden", got)
	}
	for _, h := range search(t, st, MemorySearch{Query: "rates rise"}) {
		if h.ID == old.ID {
			t.Error("a retracted memory came back from a query")
		}
	}
	if _, err := st.CurrentMemory(ctx, "wolf", "view"); !errors.Is(err, ErrNotFound) {
		t.Errorf("current view = %v, want ErrNotFound", err)
	}
	if _, err := st.Memory(ctx, "wolf", old.ID); err != nil {
		t.Errorf("reading a retracted memory by id: %v", err)
	}
	if got, err := st.CurrentMemory(ctx, "wolf", "kept"); err != nil || got.ID != other.ID {
		t.Errorf("current kept = %+v, %v", got, err)
	}
}

// Acceptance: latest_per=name returns one memory per name value — the newest — and leaves out
// memories without the label.
func TestSearchLatestPer(t *testing.T) {
	st, now := memStore(t)
	remember(t, st, now, Memory{Content: "board v1 ferry", Labels: map[string]string{"name": "board"}})
	b2 := remember(t, st, now, Memory{Content: "board v2 ferry", Labels: map[string]string{"name": "board"}})
	plan := remember(t, st, now, Memory{Content: "plan v1 ferry", Labels: map[string]string{"name": "plan"}})
	remember(t, st, now, Memory{Content: "unnamed ferry"})

	if got := hitIDs(search(t, st, MemorySearch{LatestPer: "name"})); fmt.Sprint(got) != fmt.Sprint([]string{plan.ID, b2.ID}) {
		t.Errorf("latest per name = %v, want [plan, board v2]", got)
	}
	got := hitIDs(search(t, st, MemorySearch{LatestPer: "name", Query: "ferry"}))
	if len(got) != 2 || !(got[0] == b2.ID || got[1] == b2.ID) || !(got[0] == plan.ID || got[1] == plan.ID) {
		t.Errorf("latest per name with a query = %v, want board v2 and plan", got)
	}
}

// if_current appends only while the named memory is still the newest of its name; otherwise
// nothing is written and the error names the one that is.
func TestCreateMemoryIfCurrent(t *testing.T) {
	st, now := memStore(t)
	ctx := t.Context()
	vec, _ := embed.Fake{}.Embed(ctx, "board")
	board := map[string]string{"name": "board"}
	v1 := remember(t, st, now, Memory{Content: "v1", Labels: board})

	*now = now.Add(time.Minute)
	v2, err := st.CreateMemory(ctx, Memory{Project: "wolf", Content: "v2", Labels: board}, vec, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	_, err = st.CreateMemory(ctx, Memory{Project: "wolf", Content: "v3 from stale v1", Labels: board}, vec, v1.ID)
	var nc ErrNotCurrent
	if !errors.As(err, &nc) || nc.Current != v2.ID || nc.IfCurrent != v1.ID || nc.Name != "board" {
		t.Errorf("stale if_current: err = %v", err)
	}
	if cur, err := st.CurrentMemory(ctx, "wolf", "board"); err != nil || cur.ID != v2.ID || cur.Content != "v2" {
		t.Errorf("current = %+v, %v; want v2", cur, err)
	}
	if _, err := st.CreateMemory(ctx, Memory{Project: "wolf", Content: "x"}, vec, v2.ID); err == nil {
		t.Error("if_current without a name label should be an error")
	}
	if _, err := st.CreateMemory(ctx, Memory{Project: "wolf", Content: "x", Labels: map[string]string{"name": "nothing"}}, vec, v2.ID); !errors.As(err, &nc) || nc.Current != "" {
		t.Errorf("if_current for a name nobody holds: err = %v", err)
	}
	if _, err := st.CurrentMemory(ctx, "wolf", "nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("current of an unused name: %v", err)
	}
}

// Open cannot create pgvector as an ordinary user, so it stops with the fix in the message.
func TestOpenWithoutPgvectorSaysHowToFix(t *testing.T) {
	url := os.Getenv("BOB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("BOB_TEST_DATABASE_URL not set")
	}
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("bob_test_novec_%d", time.Now().UnixNano())
	for _, sql := range []string{
		"CREATE ROLE " + name + " LOGIN PASSWORD 'x'",
		"CREATE DATABASE " + name + " OWNER " + name,
	} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Exec(context.Background(), "DROP ROLE "+name)
		admin.Close(context.Background())
	})
	u, _ := neturl.Parse(url)
	u.User = neturl.UserPassword(name, "x")
	u.Path = "/" + name
	st, err := Open(ctx, u.String())
	if st != nil {
		defer st.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "CREATE EXTENSION vector") {
		t.Fatalf("Open = %v; want the pgvector fix", err)
	}
}
