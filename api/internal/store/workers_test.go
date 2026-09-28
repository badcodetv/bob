package store

import (
	"errors"
	"fmt"
	"testing"
)

// A worker is read back with everything a turn runs with, by id or by its project and name. Tools
// left null mean the harness's default, and read as nil — not as an empty list, which would allow
// no tools at all.
func TestReadingAWorker(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf", "enc"}); err != nil {
		t.Fatal(err)
	}
	id := insertWorker(t, st, "wolf", "researcher", "claude")
	if _, err := st.db.Exec(ctx, `UPDATE workers SET model = 'opus', effort = 'high', tools = '["Read", "Bash(git:*)"]',
		prompt = 'Research the market.' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	plain := insertWorker(t, st, "wolf", "writer", "codex")

	w, err := st.WorkerByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%s %s/%s %s %s %s %q %q", w.ID, w.Project, w.Name, w.Engine, w.Model, w.Effort, w.Tools, w.Prompt); got !=
		fmt.Sprintf(`%s wolf/researcher claude opus high ["Read" "Bash(git:*)"] "Research the market."`, id) {
		t.Errorf("worker = %s", got)
	}
	if w.CreatedBy != "test" || w.UpdatedBy != "test" || w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() {
		t.Errorf("worker authorship = %+v", w)
	}
	if byName, err := st.Worker(ctx, "wolf", "researcher"); err != nil || byName.ID != id {
		t.Errorf("by name = %+v, %v; want %s", byName, err, id)
	}

	if w, err := st.WorkerByID(ctx, plain); err != nil || w.Tools != nil || w.Engine != "codex" {
		t.Errorf("worker with null tools = %+v, %v; want nil tools", w, err)
	}
	for _, missing := range []func() error{
		func() error { _, err := st.WorkerByID(ctx, "00000000-0000-0000-0000-000000000000"); return err },
		func() error { _, err := st.WorkerByID(ctx, "not-a-uuid"); return err },
		func() error { _, err := st.Worker(ctx, "enc", "researcher"); return err }, // another project's name
		func() error { _, err := st.Worker(ctx, "wolf", "nobody"); return err },
	} {
		if err := missing(); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	}
}
