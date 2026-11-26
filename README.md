# PersonalRAG

PersonalRAG is a desktop app for macOS and Windows that answers questions about your own documents and shows where each answer comes from. It indexes local folders, keeps the index up to date, and answers with citations that open the exact passage. By default everything runs on your machine with [Ollama](https://ollama.com); your documents only leave it if you explicitly choose a model on another server.

It is built with [Wails v3](https://v3.wails.io) (Go backend, React/TypeScript UI) on top of the [askyourdocs](https://github.com/dbis-ilm/askyourdocs) RAG library.

> **Status: early development.** The app works on macOS (Apple Silicon) for daily use. It builds for Windows 10/11 (x64) but has not been tested on Windows hardware yet. There are no signed releases yet; build it from source (see below).

## Features

- **Folders as collections.** Add a folder (dialog or drag and drop) and it becomes a collection. New, changed and deleted files are picked up on start, every 15 minutes and on demand; unchanged files are skipped by size, modification time and content hash.
- **Formats:** PDF, Word (.docx), Markdown, plain text and HTML. Scanned PDFs without a text layer are flagged as needing OCR instead of being indexed empty.
- **Hybrid search:** vector search (sqlite-vec) and full-text search (SQLite FTS5) merged with Reciprocal Rank Fusion. Search works without an answer model.
- **Cited answers:** the top passages are reranked by the model, the answer streams in with `[n]` citations, and each citation opens its source passage. A confidence rating says how well the sources matched.
- **Conversations:** follow-up questions are rewritten into standalone questions before searching. Conversations are stored locally with a snapshot of the cited passages, so they reopen exactly as answered.
- **Local by default:** Ollama on this machine for embeddings and answers. Alternatively an OpenAI-compatible server such as [GPUStack](https://github.com/gpustack/gpustack), LM Studio or llama.cpp, by URL and API key. A server that is not on this machine requires your explicit consent before any excerpt is sent to it.
- **Agents (MCP):** OpenCode, Claude Desktop, Claude Code, Cursor and other MCP clients can list, read, search and extract from the collections you share, read-only. Every call is logged in the app. See [Using PersonalRAG with agents](#using-personalrag-with-agents).
- **Desktop behaviour:** runs in the menu bar / notification area, closing the window keeps indexing, one instance per data directory, light and dark mode.

Planned next: a file system watcher, OCR, MCP over HTTP for the running app, signed installers. See [Roadmap](#roadmap).

## Requirements

To run:

- macOS on Apple Silicon (developed and tested on macOS 27), or Windows 10/11 (x64, untested so far)
- **Either** [Ollama](https://ollama.com) with an embedding model and, optionally, a chat model:

  ```sh
  ollama pull bge-m3       # embeddings, multilingual, works well for German
  ollama pull gemma4:12b   # answers (optional; without it the app is search-only)
  ```

- **or** an OpenAI-compatible server (GPUStack, LM Studio, llama.cpp) with an embedding model and optionally a chat model.

To build:

- Go 1.26 or later
- Node.js 20 or later
- Wails CLI v3.1.0: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0` (make sure `$(go env GOPATH)/bin` is on your `PATH`)
- macOS: Xcode command line tools; Xcode itself when regenerating the icon (`actool` builds the macOS 26 `Assets.car`)

## Build and run

```sh
wails3 dev       # development mode with hot reload
wails3 build     # native binary in bin/PersonalRAG (bin\PersonalRAG.exe on Windows)
wails3 package   # macOS: bin/PersonalRAG.app with icon
```

On macOS, start the bundle (`open bin/PersonalRAG.app`) to get the Dock icon. On first start a setup assistant checks the model connection, lets you pick the models and asks for the first folder.

## Data, privacy and logs

| What | Where |
| --- | --- |
| Index, conversations (`index.db`), settings (`settings.json`), logs (`logs/`) | macOS: `~/Library/Application Support/PersonalRAG` · Windows: `%APPDATA%\PersonalRAG` |
| API key for an OpenAI-compatible server | macOS Keychain · Windows Credential Manager (never in `settings.json`) |

- PersonalRAG never modifies your documents. The index holds text excerpts of them in plain text, protected like the rest of your user profile.
- With Ollama, nothing leaves your machine. With a model on another server, questions and excerpts are sent there; the app asks for consent per server and shows the server in the sidebar while it is in use.
- The log rotates at 5 MB and keeps three older files. Questions and document text are logged only at level `debug`. Crashes are written to `logs/crash*.log` and reported on the next start.
- Settings › Diagnose shows a diagnostic report (versions, settings, index statistics, last log lines, home directory replaced by `~`) before you copy it into a bug report.

Environment variables:

| Variable | Effect |
| --- | --- |
| `PERSONALRAG_DATA` | Use another data directory, e.g. for testing. Each data directory has its own single-instance lock. |
| `PERSONALRAG_LOG_LEVEL` | `debug`, `info`, `warn` or `error`; overrides the setting. |

## Using PersonalRAG with agents

An MCP client starts the app binary with `--mcp` and talks to it over stdio; the app window does not need to be open. Agents see only the collections you share under **Agents** in the app, and every tool call appears in the access log there. The same screen shows ready-made configuration for each client with the right path to the binary.

OpenCode (`opencode.json` in a project or `~/.config/opencode/opencode.json`):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "personalrag": {
      "type": "local",
      "command": ["/Applications/PersonalRAG.app/Contents/MacOS/PersonalRAG", "--mcp"],
      "enabled": true
    }
  }
}
```

Claude Code: `claude mcp add personalrag -- /Applications/PersonalRAG.app/Contents/MacOS/PersonalRAG --mcp`

| Tool | Purpose |
| --- | --- |
| `list_collections` | Shared collections and their document counts |
| `list_documents` | Every document of the shared collections, filterable by collection, folder, format and date, paged. Includes documents that could not be read (e.g. scans needing OCR), so an evaluation can report what it did not cover |
| `read_document` | Full text of one document, paged by characters, with `[Seite n]` page markers |
| `search_documents` | Hybrid search; returns the most relevant passages, not all matches |
| `extract` | Applies an instruction to one whole document with *your* model (e.g. "list the names under Anwesend") and returns only the result |

Search answers questions; it does not cover every document. For evaluations over a whole collection, such as listing all participants of all meetings and counting how often each attended, let the agent call `list_documents`, then `extract` (or `read_document`) for each document, and count with code. The server tells the client this in its instructions. With `extract`, the document text stays with your model and only the result reaches the agent; with `read_document`, the agent's model sees the text. If the agent runs on a cloud model, that text goes to its provider.

## How it works

```
Folder ──► extract (PDF, .docx, Markdown, text, HTML) ──► chunk ──► embed ──► SQLite
                                                                         ├─ chunks
                                                                         ├─ FTS5 full-text index
                                                                         └─ sqlite-vec vectors

Question ──► (follow-up: rewrite as standalone) ──► vector + full-text search ──► RRF
         ──► sources shown at once ──► rerank ──► streamed answer with [n] citations
```

Indexing runs on one background worker so it never competes with itself for the embedding model; dropped files go to the front of the queue. Extraction, chunking, reranking, prompt-injection hardening and the model providers come from askyourdocs. The SQLite index is CGo-free (`modernc.org/sqlite` with its bundled sqlite-vec) and implements `askyourdocs.VectorStore`.

## Project layout

| Path | Contents |
| --- | --- |
| `main.go` | Window, tray, file drop, single instance, logging setup |
| `appservice.go` | API bound to the UI: lifecycle of index, indexer and engine; collections, search, answers, conversations, diagnostics |
| `connections.go` | Model providers (Ollama, OpenAI-compatible), model selection with consent for remote servers, exclusion patterns |
| `internal/agent` | MCP server and its tools (official Go SDK), sharing checks, access log |
| `internal/store` | SQLite index with FTS5 and sqlite-vec |
| `internal/ingest` | Text extraction per format and the `IngestSource` for local files |
| `internal/indexer` | Folder sync, priority queue, single worker |
| `internal/rag` | Hybrid search, reranking, streamed answers; embedded prompts; Ollama and OpenAI-compatible API clients |
| `internal/history` | Stored conversations |
| `internal/migrate` | Versioned schema migrations per component, with a backup before migrating |
| `internal/config` | Settings, data directory, single-instance ID |
| `internal/secrets` | API keys in the system keychain |
| `internal/logging` | Rotating log, runtime log level, crash file |
| `internal/paths` | Path comparison by platform rules (case-insensitive on Windows) |
| `frontend/src` | React UI: ask, sources, agents, settings and setup assistant |
| `build/` | Platform files generated by Wails; icon sources in `build/icon-src` |

Generic parts (the SQLite store, Markdown and text extraction, the queue priority) are meant to move into askyourdocs once they have settled.

## Development

```sh
go test ./...                                    # offline, with a fake embedder
PERSONALRAG_OLLAMA=1 go test ./... -run Ollama   # end to end against a local Ollama
```

The end-to-end tests use `PERSONALRAG_EMBED` (default `bge-m3`) and `PERSONALRAG_LLM` (default `gemma4:12b`). `testdata/prototype-b0695f2.db` is a database from the first commit; `TestUpgradeFromPrototype` checks that schema migrations keep its contents.

After changing exported methods or types of the bound services, regenerate the TypeScript bindings with `wails3 generate bindings -ts` (`wails3 build` does this as well).

CI (`.github/workflows/ci.yml`) runs on macOS and Windows: frontend type check and build, `go vet`, Go tests (with the race detector on macOS) and `wails3 build`.

To change the icon, edit the SVGs in `build/icon-src`, render `build/appicon.png` from `appicon.svg` (1024 × 1024) and run `wails3 generate icons` as in `build/Taskfile.yml` (`common:generate:icons`).

## Roadmap

1. **Foundation and Windows:** run and test on Windows hardware; CI on both platforms.
2. **Index and answer quality:** file system watcher, cloud placeholders (iCloud, OneDrive), evaluation on a real corpus, OCR, more formats (.eml, .pptx, .xlsx), filters by file type and date.
3. **MCP for agents:** stdio server with read-only tools, per-collection sharing and access log are done; next: Streamable HTTP for the running app with token and origin checks.
4. **Distribution:** signed and notarised macOS package, Windows installer, update notice, autostart, quick-ask window.

## License

Apache License 2.0.
