package store

import (
	"errors"
	"testing"
)

func mkWorker(project, name string) Worker {
	return Worker{Project: project, Name: name, Engine: "claude", Model: "", Effort: "",
		Tools: []string{"bash", "read"}, Prompt: "be helpful"}
}

// CreateWorker writes the worker and one version, and Labels is always {} for now.
func TestCreateWorker(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	w, err := st.CreateWorker(ctx, mkWorker("wolf", "researcher"), "kai", "new worker")
	if err != nil {
		t.Fatal(err)
	}
	if w.ID == "" || w.Project != "wolf" || w.Name != "researcher" || w.CreatedBy != "kai" || w.UpdatedBy != "kai" {
		t.Errorf("created worker = %+v", w)
	}
	if len(w.Tools) != 2 || w.Tools[0] != "bash" {
		t.Errorf("tools = %v", w.Tools)
	}
	if w.Labels == nil || len(w.Labels) != 0 {
		t.Errorf("labels = %v, want empty map", w.Labels)
	}

	versions, err := st.WorkerVersions(ctx, w.ID)
	if err != nil || len(versions) != 1 || versions[0].Action != "create" || versions[0].Why != "new worker" || versions[0].ChangedBy != "kai" {
		t.Errorf("versions = %v, %v; want one create", versions, err)
	}

	got, err := st.Worker(ctx, "wolf", "researcher")
	if err != nil || got.ID != w.ID {
		t.Errorf("Worker(project,name) = %+v, %v", got, err)
	}
	byID, err := st.WorkerByID(ctx, w.ID)
	if err != nil || byID.ID != w.ID {
		t.Errorf("WorkerByID = %+v, %v", byID, err)
	}
}

// A duplicate (project, name) is a conflict, not a generic error.
func TestCreateWorkerDuplicateName(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorker(ctx, mkWorker("wolf", "researcher"), "kai", "new worker"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorker(ctx, mkWorker("wolf", "researcher"), "kai", "again"); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate name error = %v, want ErrConflict", err)
	}
}

// An empty why is rejected, for create, update, delete and the project prompt.
func TestEmptyWhyIsRejected(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorker(ctx, mkWorker("wolf", "researcher"), "kai", ""); err == nil {
		t.Error("CreateWorker with empty why should fail")
	}
	w, err := st.CreateWorker(ctx, mkWorker("wolf", "researcher"), "kai", "new worker")
	if err != nil {
		t.Fatal(err)
	}
	w.Prompt = "changed"
	if _, err := st.UpdateWorker(ctx, w, "kai", ""); err == nil {
		t.Error("UpdateWorker with empty why should fail")
	}
	if err := st.DeleteWorker(ctx, "wolf", "researcher", "kai", ""); err == nil {
		t.Error("DeleteWorker with empty why should fail")
	}
	if err := st.SetProjectPrompt(ctx, "wolf", "be brief", "kai", ""); err == nil {
		t.Error("SetProjectPrompt with empty why should fail")
	}
}

// create -> 1 version, update -> 2, delete -> 3, and WorkerVersions(id) still returns all three
// after the worker itself is gone; deleting nulls the session's worker_id and drops its schedules.
func TestWorkerLifecycleAndVersions(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	w, err := st.CreateWorker(ctx, mkWorker("wolf", "researcher"), "kai", "new worker")
	if err != nil {
		t.Fatal(err)
	}

	sess, err := st.CreateSession(ctx, "wolf", &w.ID, "researcher", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	sch, err := st.CreateSchedule(ctx, Schedule{Project: "wolf", Name: "daily", WorkerID: w.ID, Cron: "0 6 * * *",
		Timezone: "UTC", Message: "go", Enabled: true, KeepSessions: 5})
	if err != nil {
		t.Fatal(err)
	}

	w.Model = "opus"
	updated, err := st.UpdateWorker(ctx, w, "kai", "switch model")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Model != "opus" || updated.UpdatedBy != "kai" {
		t.Errorf("updated worker = %+v", updated)
	}

	versions, err := st.WorkerVersions(ctx, w.ID)
	if err != nil || len(versions) != 2 || versions[0].Action != "update" || versions[1].Action != "create" {
		t.Fatalf("versions after update = %v, %v; want [update, create]", versions, err)
	}

	if err := st.DeleteWorker(ctx, "wolf", "researcher", "kai", "retiring"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Worker(ctx, "wolf", "researcher"); !errors.Is(err, ErrNotFound) {
		t.Errorf("worker after delete = %v, want ErrNotFound", err)
	}

	versions, err = st.WorkerVersions(ctx, w.ID)
	if err != nil || len(versions) != 3 || versions[0].Action != "delete" {
		t.Fatalf("versions after delete = %v, %v; want 3, newest delete", versions, err)
	}

	got, err := st.Session(ctx, sess.ID)
	if err != nil || got.WorkerID != nil {
		t.Errorf("session after its worker deleted = %+v, %v; want worker_id nulled", got, err)
	}
	if _, err := st.Schedule(ctx, sch.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("schedule after its worker deleted = %v, want ErrNotFound", err)
	}
}

// Workers lists a project's workers by name.
func TestWorkers(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorker(ctx, mkWorker("wolf", "zeta"), "kai", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWorker(ctx, mkWorker("wolf", "alpha"), "kai", "a"); err != nil {
		t.Fatal(err)
	}
	ws, err := st.Workers(ctx, "wolf")
	if err != nil || len(ws) != 2 || ws[0].Name != "alpha" || ws[1].Name != "zeta" {
		t.Errorf("workers = %v, %v; want [alpha, zeta]", ws, err)
	}
}

// UpdateWorker on an unknown worker is ErrNotFound.
func TestUpdateWorkerNotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateWorker(ctx, mkWorker("wolf", "ghost"), "kai", "why"); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of unknown worker = %v, want ErrNotFound", err)
	}
}

// The project prompt round-trips through SetProjectPrompt, and every change is versioned,
// newest first.
func TestProjectPromptVersions(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectPrompt(ctx, "wolf", "be brief", "kai", "first prompt"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectPrompt(ctx, "wolf", "be very brief", "kai", "tighten it"); err != nil {
		t.Fatal(err)
	}
	p, err := st.Project(ctx, "wolf")
	if err != nil || p.Prompt != "be very brief" {
		t.Errorf("project prompt = %+v, %v; want the latest", p, err)
	}
	versions, err := st.ProjectPromptVersions(ctx, "wolf")
	if err != nil || len(versions) != 2 || versions[0].Prompt != "be very brief" || versions[1].Prompt != "be brief" {
		t.Fatalf("prompt versions = %v, %v; want [very brief, brief]", versions, err)
	}
	if versions[0].Why != "tighten it" || versions[0].ChangedBy != "kai" {
		t.Errorf("version[0] = %+v", versions[0])
	}
}
