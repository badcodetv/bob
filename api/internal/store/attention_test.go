package store

import (
	"errors"
	"testing"
)

func TestAttentionLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	if err := st.ReconcileProjects(ctx, []string{"wolf", "enc"}); err != nil {
		t.Fatal(err)
	}
	chat, err := st.CreateSession(ctx, "wolf", nil, "", "claude", "", "")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := st.CreateSession(ctx, "enc", nil, "", "claude", "", "")

	a, err := st.CreateAttention(ctx, chat.ID, "", "may I post it?", "ask")
	if err != nil || a.Project != "wolf" || a.SessionID != chat.ID || a.Kind != "ask" || a.ClosedAt != nil {
		t.Fatalf("create = %+v, %v", a, err)
	}
	if _, err := st.CreateAttention(ctx, other.ID, "poet", "fyi", "notice"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAttention(ctx, chat.ID, "", "x", "shout"); err == nil {
		t.Error("an unknown kind was accepted")
	}

	open, err := st.Attentions(ctx, "wolf", true)
	if err != nil || len(open) != 1 || open[0].ID != a.ID {
		t.Fatalf("open wolf = %+v, %v", open, err)
	}
	ss, _ := st.Sessions(ctx, "wolf")
	if len(ss) != 1 || !ss[0].Attention {
		t.Errorf("session list attention = %+v", ss)
	}

	n, err := st.CloseSessionAttention(ctx, chat.ID, "kai@example.com")
	if err != nil || n != 1 {
		t.Fatalf("close session = %d, %v", n, err)
	}
	got, _ := st.Attention(ctx, a.ID)
	if got.ClosedAt == nil || got.CloseReason != "answered" || got.ClosedBy != "kai@example.com" {
		t.Errorf("closed = %+v", got)
	}
	if open, _ := st.Attentions(ctx, "wolf", true); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
	if all, _ := st.Attentions(ctx, "wolf", false); len(all) != 1 {
		t.Errorf("all = %+v", all)
	}
	if ss, _ := st.Sessions(ctx, "wolf"); ss[0].Attention {
		t.Error("attention flag did not clear")
	}

	// Dismissing closes; closing again leaves the first close alone; the other project's is untouched.
	b, _ := st.CreateAttention(ctx, chat.ID, "", "again", "ask")
	d, err := st.CloseAttention(ctx, b.ID, "jack@example.com", "dismissed")
	if err != nil || d.CloseReason != "dismissed" || d.ClosedBy != "jack@example.com" {
		t.Fatalf("dismiss = %+v, %v", d, err)
	}
	d2, err := st.CloseAttention(ctx, b.ID, "someone@example.com", "answered")
	if err != nil || d2.CloseReason != "dismissed" || d2.ClosedBy != "jack@example.com" {
		t.Errorf("second close changed it: %+v, %v", d2, err)
	}
	if _, err := st.CloseAttention(ctx, "not-a-uuid", "x", "dismissed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bad id = %v, want ErrNotFound", err)
	}
	if open, _ := st.Attentions(ctx, "enc", true); len(open) != 1 {
		t.Errorf("enc open = %+v", open)
	}

	// Deleting the chat deletes its requests.
	if err := st.DeleteSession(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if open, _ := st.Attentions(ctx, "enc", false); len(open) != 0 {
		t.Errorf("requests outlived their chat: %+v", open)
	}
}
