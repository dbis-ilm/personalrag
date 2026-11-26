package history

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"personalrag/internal/store"
)

func TestConversationLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	st, err := store.Open(path, nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(st.DB(), "")
	if err != nil {
		t.Fatal(err)
	}

	c, err := h.Create(ctx, "c1", "Wie lange ist die Kündigungsfrist für meine Wohnung in der Schillerstraße, und gilt noch der Verzicht?", "vertraege")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.Title, "…") || len([]rune(c.Title)) > 72 {
		t.Fatalf("title not shortened: %q", c.Title)
	}
	t1, err := h.AddTurn(ctx, Turn{ConversationID: "c1", Question: "Kündigungsfrist?", Mode: "answer", Sources: []byte(`[{"n":1}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SavePartial(ctx, t1, "Drei Mo"); err != nil {
		t.Fatal(err)
	}
	if err := h.Finish(ctx, t1, "Drei Monate [1].", "hoch", "1 von 3", ""); err != nil {
		t.Fatal(err)
	}
	t2, _ := h.AddTurn(ctx, Turn{ConversationID: "c1", Question: "Und für den Vermieter?", Standalone: "Kündigungsfrist Vermieter?", Mode: "answer"})
	_ = h.SavePartial(ctx, t2, "Für den Vermieter")
	if _, err := h.AddTurn(ctx, Turn{ConversationID: "missing", Question: "x", Mode: "search"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("turn for unknown conversation: %v", err)
	}

	// Simulate a restart while turn 2 was streaming.
	st.Close()
	st, _ = store.Open(path, nil, "", 0)
	defer st.Close()
	h, _ = New(st.DB(), "")
	if err := h.MarkInterrupted(ctx); err != nil {
		t.Fatal(err)
	}

	_, turns, err := h.Get(ctx, "c1")
	if err != nil || len(turns) != 2 {
		t.Fatalf("Get: %d turns, %v", len(turns), err)
	}
	if turns[0].Answer != "Drei Monate [1]." || turns[0].Confidence != "hoch" || string(turns[0].Sources) != `[{"n":1}]` || turns[0].Error != "" {
		t.Fatalf("turn 1: %+v", turns[0])
	}
	if !turns[1].Done || turns[1].Answer != "Für den Vermieter" || turns[1].Error == "" || turns[1].Standalone == "" {
		t.Fatalf("interrupted turn: %+v", turns[1])
	}

	h.Create(ctx, "c2", "Zweite", "")
	list, _ := h.List(ctx, 10)
	if len(list) != 2 || list[0].ID != "c2" {
		t.Fatalf("list order: %+v", list)
	}
	if err := h.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Turn(ctx, t1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("turns not deleted with conversation: %v", err)
	}
}
