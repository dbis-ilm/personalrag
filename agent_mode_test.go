package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"

	"github.com/dbis-ilm/personalrag/internal/agent"
	"github.com/dbis-ilm/personalrag/internal/history"
	"github.com/dbis-ilm/personalrag/internal/i18n"
	"github.com/dbis-ilm/personalrag/internal/indexer"
	"github.com/dbis-ilm/personalrag/internal/rag"
	"github.com/dbis-ilm/personalrag/internal/store"
	"github.com/dbis-ilm/personalrag/internal/testutil"
)

// toolModel acts like an agent model: first it asks for list_documents,
// then it answers with the number of documents the tool returned. A task
// containing "WARTE" blocks until cancelled, to test stopping.
func toolModel(g *genkit.Genkit) ai.Model {
	return genkit.DefineModel(g, "test/agent", &ai.ModelOptions{Supports: &ai.ModelSupports{Multiturn: true, SystemRole: true, Tools: true}},
		func(ctx context.Context, req *ai.ModelRequest, _ ai.ModelStreamCallback) (*ai.ModelResponse, error) {
			var prompt strings.Builder
			for _, m := range req.Messages {
				prompt.WriteString(m.Text())
			}
			if strings.Contains(prompt.String(), "WARTE") {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			last := req.Messages[len(req.Messages)-1]
			if last.Role == ai.RoleTool {
				b, _ := json.Marshal(last.Content[0].ToolResponse.Output)
				var out struct {
					Result struct {
						Total int `json:"total"`
					} `json:"result"`
				}
				json.Unmarshal(b, &out)
				return &ai.ModelResponse{Message: ai.NewModelTextMessage(fmt.Sprintf("Die Sammlung enthält %d Dokumente.", out.Result.Total))}, nil
			}
			return &ai.ModelResponse{Message: &ai.Message{Role: ai.RoleModel, Content: []*ai.Part{
				ai.NewToolRequestPart(&ai.ToolRequest{Name: "list_documents", Input: map[string]any{"collection_id": "p"}}),
			}}}, nil
		})
}

func TestAgentMode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "Protokolle")
	os.MkdirAll(root, 0o755)
	for i := range 3 {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("p%d.md", i)), []byte(fmt.Sprintf("# Sitzung %d\n\nAnwesend: A, B\n", i)), 0o644)
	}
	st, err := store.Open(filepath.Join(dir, "index.db"), testutil.FakeEmbedder{}, "fake", testutil.FakeDim)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	coll := store.Collection{ID: "p", Name: "Protokolle", Color: "#000", Folders: []string{root}}
	if err := st.CreateCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	g := genkit.Init(ctx, genkit.WithPromptFS(rag.PromptFS), genkit.WithPromptDir("prompts"))
	ix := indexer.New(g, st, nil, nil)
	go ix.Run(ctx)
	ix.ScanCollection(ctx, coll)
	for deadline := time.Now().Add(10 * time.Second); ix.Progress().Active && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	eng, err := rag.New(g, st, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := history.New(st.DB(), "")
	if err != nil {
		t.Fatal(err)
	}
	rt := &agentRuntime{spec: "test/agent", g: g, model: toolModel(g), tools: agent.New(st, eng, "test").InApp().Tools(g)}
	s := &AppService{appCtx: ctx, core: &core{st: st, eng: eng, hist: hist, agent: rt}, searches: map[int64]cachedSearch{}}

	finished := func(turnID int64) history.Turn {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			s.mu.Lock()
			_, running := s.agentRuns[turnID]
			s.mu.Unlock()
			if tt, err := hist.Turn(ctx, turnID); err == nil && tt.Done && !running {
				return tt
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("agent task did not finish")
		return history.Turn{}
	}

	start, err := s.Agent("", "Wie viele Protokolle gibt es?", "p")
	if err != nil {
		t.Fatal(err)
	}
	turn := finished(start.TurnID)
	if turn.Mode != "agent" || turn.Answer != "Die Sammlung enthält 3 Dokumente." || turn.Error != "" || !strings.HasPrefix(turn.Note, "1 ") {
		t.Fatalf("turn: %+v", turn)
	}
	log, _ := st.AccessLog(ctx, 10)
	if len(log) == 0 || log[0].Client != agent.AgentClient || log[0].Tool != "list_documents" {
		t.Fatalf("access log: %+v", log)
	}

	// A follow-up in the same conversation carries the earlier turn.
	if p := agentPrompt("Und als Tabelle?", "p", "Protokolle", []history.Turn{turn}, false); !strings.Contains(p, "Die Sammlung enthält 3 Dokumente.") || !strings.Contains(p, `"p"`) {
		t.Fatalf("prompt: %s", p)
	}
	if p := agentPrompt("x", "", "", nil, true); !strings.Contains(p, "no read_document") {
		t.Fatalf("text-local prompt: %s", p)
	}

	// Stopping a running task.
	start, err = s.Agent(start.ConversationID, "WARTE auf nichts", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	s.StopAgent(start.TurnID)
	if turn := finished(start.TurnID); turn.Error != i18n.T("agent.stopped") {
		t.Fatalf("stopped turn: %+v", turn)
	}

	// Without an agent model the mode is refused.
	s.core.agent = nil
	if _, err := s.Agent("", "x", ""); err == nil {
		t.Fatal("agent ran without a model")
	}
}
