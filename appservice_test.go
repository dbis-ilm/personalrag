package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestServiceWithOllama drives the service like the UI does, without a
// window: setup, add folder, index, search. Opt in with PERSONALRAG_OLLAMA=1.
func TestServiceWithOllama(t *testing.T) {
	if os.Getenv("PERSONALRAG_OLLAMA") == "" {
		t.Skip("set PERSONALRAG_OLLAMA=1 to run against a local Ollama")
	}
	dataDir := t.TempDir()
	t.Setenv("PERSONALRAG_DATA", dataDir)
	docs := filepath.Join(t.TempDir(), "Verträge")
	os.MkdirAll(docs, 0o755)
	os.WriteFile(filepath.Join(docs, "mietvertrag.md"), []byte("# Mietvertrag\n\n## § 5 Kündigung\n\nDie Kündigungsfrist beträgt drei Monate zum Monatsende.\n"), 0o644)
	os.WriteFile(filepath.Join(docs, "kaputt.pdf"), []byte("kein pdf"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewAppService(dataDir, nil)
	if err := s.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	defer s.ServiceShutdown()

	if st := s.Status(); st.SetupDone || !st.Ready || !st.SearchOnly {
		t.Fatalf("fresh status: %+v", st)
	}
	rt := s.RuntimeInfo()
	if !rt.Providers[0].OK || rt.SuggestedEmbedding == "" {
		t.Skipf("ollama not usable: %+v", rt)
	}
	if err := s.SaveModels(rt.SuggestedLLM, rt.SuggestedEmbedding, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "core restart", 20*time.Second, func() bool { st := s.Status(); return st.Ready && st.Embedding != "" })
	if err := s.FinishSetup(); err != nil {
		t.Fatal(err)
	}

	coll, err := s.AddFolderPath(docs)
	if err != nil || coll.ID != "vertraege" {
		t.Fatalf("AddFolderPath: %+v, %v", coll, err)
	}
	if _, err := s.AddFolderPath(docs); err == nil {
		t.Fatal("adding the same folder twice must fail")
	}
	waitFor(t, "indexing", 60*time.Second, func() bool {
		d, _ := s.Documents("")
		done := 0
		for _, x := range d {
			if x.Status == "ok" || x.Status == "error" {
				done++
			}
		}
		return done == 2
	})
	docsList, _ := s.Documents("")
	for _, d := range docsList {
		t.Logf("doc %s: %s %s", d.Name, d.Status, d.Note)
	}
	if bad, _ := s.Documents("error"); len(bad) != 1 || bad[0].Name != "kaputt.pdf" {
		t.Fatalf("broken PDF should be marked as error: %+v", bad)
	}

	res, err := s.Search("", "Wie lange ist die Kündigungsfrist?", coll.ID, "answer")
	if err != nil || len(res.Sources) == 0 || res.Notice != "" || res.ConversationID == "" || res.TurnID == 0 {
		t.Fatalf("Search: %+v, %v", res, err)
	}
	top := res.Sources[0]
	t.Logf("top: %s · %s · score %.2f · %q", top.File, top.Location, top.Score, top.Highlight)
	if top.File != "mietvertrag.md" || !strings.HasPrefix(top.Highlight, "Die Kündigungsfrist") || top.Location == "" || strings.Contains(top.Before, "[") {
		t.Fatalf("top source wrong: %+v", top)
	}
	convID := res.ConversationID
	if s.Status().SearchOnly {
		t.Skip("no answer model installed")
	}
	if err := s.Ask(res.TurnID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "answer stored", 3*time.Minute, func() bool {
		d, _ := s.Conversation(convID)
		return len(d.Turns) == 1 && d.Turns[0].Done
	})

	// Follow-up: rewritten into a standalone question, stored as turn 2.
	res2, err := s.Search(convID, "Und muss sie schriftlich erfolgen?", coll.ID, "search")
	if err != nil || res2.ConversationID != convID {
		t.Fatalf("follow-up: %+v, %v", res2, err)
	}
	t.Logf("follow-up rewritten to %q", res2.Standalone)
	if res2.Standalone == "" {
		t.Errorf("follow-up was not rewritten")
	}

	if f, _ := s.Folders(); len(f) != 1 || f[0].Files != 2 {
		t.Fatalf("folders: %+v", f)
	}

	// Settings survive a restart.
	s.ServiceShutdown()
	s2 := NewAppService(dataDir, nil)
	if err := s2.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	defer s2.ServiceShutdown()
	if st := s2.Status(); !st.SetupDone || !st.Ready || st.Embedding == "" {
		t.Fatalf("status after restart: %+v", st)
	}
	if c, _ := s2.Collections(); len(c) != 1 || c[0].DocCount != 2 {
		t.Fatalf("collections after restart: %+v", c)
	}
	list, _ := s2.Conversations()
	if len(list) != 1 || list[0].ID != convID || !strings.HasPrefix(list[0].Title, "Wie lange") {
		t.Fatalf("conversations after restart: %+v", list)
	}
	d, err := s2.Conversation(convID)
	if err != nil || len(d.Turns) != 2 {
		t.Fatalf("conversation after restart: %+v, %v", d, err)
	}
	t1 := d.Turns[0]
	t.Logf("stored answer: %q (%s)", t1.Answer, t1.Confidence)
	if !strings.Contains(strings.ToLower(t1.Answer), "drei monate") || t1.Confidence == "" || len(t1.Sources) == 0 || t1.Sources[0].File != "mietvertrag.md" {
		t.Fatalf("turn 1 not persisted correctly: %+v", t1)
	}
	if d.Turns[1].Mode != "search" || !d.Turns[1].Done || d.Turns[1].Standalone == "" {
		t.Fatalf("turn 2 not persisted correctly: %+v", d.Turns[1])
	}
	if err := s2.DeleteConversation(convID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s2.Conversations(); len(list) != 0 {
		t.Fatalf("conversation not deleted: %+v", list)
	}
}

func TestOpenHelpOnlyKnownPages(t *testing.T) {
	s := &AppService{}
	if err := s.OpenHelp("https://example.org"); err == nil {
		t.Error("opened an arbitrary address")
	}
	for page, u := range helpPages {
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("%s: %s is not https", page, u)
		}
	}
}
