package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dbis-ilm/personalrag/internal/agent"
	"github.com/dbis-ilm/personalrag/internal/store"
)

// TestMCPOverStdio builds the binary, prepares a data directory with the
// app's own service (real Ollama models), then starts "PersonalRAG --mcp"
// as an agent client would and runs the participant-count use case with the
// real local model. Opt in with PERSONALRAG_OLLAMA=1.
func TestMCPOverStdio(t *testing.T) {
	if os.Getenv("PERSONALRAG_OLLAMA") == "" {
		t.Skip("set PERSONALRAG_OLLAMA=1 to run against a local Ollama")
	}
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	os.MkdirAll(dataDir, 0o700)
	docs := filepath.Join(base, "Protokolle")
	os.MkdirAll(docs, 0o755)
	for name, body := range map[string]string{
		"sitzung-1.md": "# Sitzung vom 3. März\n\nAnwesend: Anna Berg, Carl Dorn\n\nTOP 1: Das Budget wird angenommen.\n",
		"sitzung-2.md": "# Sitzung vom 7. April\n\nAnwesend: Anna Berg, Eva Fuchs\n\nTOP 1: Der Termin wird verschoben.\n",
		"sitzung-3.md": "# Sitzung vom 5. Mai\n\nAnwesend: Anna Berg, Carl Dorn, Eva Fuchs\n\nTOP 1: Bericht der Kasse.\n",
	} {
		os.WriteFile(filepath.Join(docs, name), []byte(body), 0o644)
	}

	// 1. Set up like a user would: models, folder, sharing.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	app := NewAppService(dataDir, nil)
	if err := app.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	rt := app.RuntimeInfo()
	if !rt.Providers[0].OK || rt.SuggestedEmbedding == "" || rt.SuggestedLLM == "" {
		app.ServiceShutdown()
		t.Skipf("ollama models not available: %+v", rt)
	}
	if err := app.SaveModels(rt.SuggestedLLM, rt.SuggestedEmbedding, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "core", 30*time.Second, func() bool { return app.Status().Ready && app.Status().Embedding != "" })
	coll, err := app.AddFolderPath(docs)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "indexing", 2*time.Minute, func() bool {
		d, _ := app.Documents(store.StatusOK)
		return len(d) == 3
	})
	if err := app.SetCollectionShared(coll.ID, true); err != nil {
		t.Fatal(err)
	}
	app.ServiceShutdown() // the app does not need to run for agents

	// 2. Build the binary and start it as an MCP server.
	bin := filepath.Join(base, "PersonalRAG")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "--mcp")
	cmd.Env = append(os.Environ(), "PERSONALRAG_DATA="+dataDir)
	client := mcp.NewClient(&mcp.Implementation{Name: "opencode"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "check,extract,list_collections,list_documents,read_document,search_documents" {
		t.Fatalf("tools: %v", names)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "list_documents") {
		t.Error("server instructions missing")
	}

	call := func(tool string, args map[string]any, out any) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("%s: tool error: %s", tool, text)
		}
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
	}

	// 3. The use case with the real local model.
	var list agent.ListDocumentsOut
	call("list_documents", nil, &list)
	if list.Total != 3 {
		t.Fatalf("list_documents: %+v", list)
	}
	counts := map[string]int{}
	split := regexp.MustCompile(`[,;\n]+`)
	for _, d := range list.Documents {
		var ex agent.ExtractOut
		started := time.Now()
		call("extract", map[string]any{"document_id": d.ID, "instruction": "Liste die Namen der Personen unter 'Anwesend', durch Komma getrennt, ohne weitere Wörter."}, &ex)
		t.Logf("%s (%s): %q", d.Name, time.Since(started).Round(time.Millisecond), ex.Parts[0].Result)
		for _, n := range split.Split(ex.Parts[0].Result, -1) {
			n = strings.Trim(strings.TrimSpace(n), "-*•. ")
			if n != "" {
				counts[n]++
			}
		}
	}
	want := map[string]int{"Anna Berg": 3, "Carl Dorn": 2, "Eva Fuchs": 2}
	for n, c := range want {
		if counts[n] != c {
			t.Errorf("%s: %d, want %d (all: %v)", n, counts[n], c, counts)
		}
	}
	if len(counts) != len(want) {
		t.Errorf("unexpected names: %v", counts)
	}

	var found agent.SearchOut
	call("search_documents", map[string]any{"query": "Wann wurde das Budget angenommen?"}, &found)
	if len(found.Passages) == 0 || !strings.Contains(found.Passages[0].Text, "Budget") {
		t.Errorf("search: %+v", found.Passages)
	}
	cs.Close()

	// The calls are in the access log the app shows.
	app2 := NewAppService(dataDir, nil)
	if err := app2.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	defer app2.ServiceShutdown()
	log := app2.AccessLog()
	if len(log) < 5 || log[0].Client != "opencode" {
		t.Fatalf("access log: %+v", log)
	}
}
