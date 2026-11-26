# PersonalRAG

Desktop-App (Wails v3 + React/TypeScript) für die lokale Suche in eigenen Dokumenten mit belegten Antworten. Konzept: `../PersonalRAG – Grobkonzept.md`.

**Stand:** Phase 1 lauffähig: Ordner indexieren, hybride Suche, Antworten mit Zitaten über ein lokales Ollama-Modell. MCP-Server, OCR und Dateisystem-Watcher fehlen noch.

## Voraussetzungen

- Go ≥ 1.26, Node ≥ 20, Wails-CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest` (getestet mit v3.1.0)
- [Ollama](https://ollama.com) mit einem Embedding-Modell (empfohlen `ollama pull bge-m3`) und optional einem Antwortmodell (z. B. `gemma4:12b`)

## Starten

```sh
wails3 dev      # Entwicklungsmodus mit Hot-Reload
wails3 build    # natives Binary nach bin/personalrag
```

Beim ersten Start führt ein Assistent durch Runtime-Check, Modellwahl und den ersten Ordner. Daten liegen in `~/Library/Application Support/PersonalRAG` bzw. `%APPDATA%\PersonalRAG` (`settings.json`, `index.db`). Mit `PERSONALRAG_DATA=<dir>` lässt sich ein anderes Verzeichnis nutzen, z. B. zum Testen.

## Aufbau

| Pfad | Inhalt |
| --- | --- |
| `main.go` | Fenster, Tray, Datei-Drop, Einzelinstanz je Datenverzeichnis, Schließen blendet nur aus |
| `appservice.go` | API für die UI (Wails-Bindings), Lebenszyklus von Index, Indexer und Engine |
| `internal/store` | `SqliteStore`-Kandidat: SQLite (modernc, CGo-frei) mit FTS5 und sqlite-vec; erfüllt `askyourdocs.VectorStore` |
| `internal/ingest` | Extraktion (PDF, .docx über askyourdocs; Markdown, Text, HTML) und `IngestSource` je Datei |
| `internal/indexer` | Ordner-Abgleich (Größe/mtime, Hash bei Änderung), Warteschlange mit Priorität, ein Worker, Genkit-Flow `indexFile` |
| `internal/rag` | Hybride Suche (Vektor + FTS5, RRF), LLM-Reranking, gestreamte Antwort mit `[n]`-Zitaten; Prompts eingebettet |
| `internal/config` | Einstellungen und Datenverzeichnis |
| `frontend/src/` | Ansichten Fragen, Quellen, Agenten, Einstellungen/Assistent |

Generische Teile (`internal/store`, Markdown/Text-Extraktion, Prioritäten der Warteschlange) sollen nach askyourdocs wandern.

## Tests

```sh
go test ./...                                   # offline, mit Fake-Embedder
PERSONALRAG_OLLAMA=1 go test ./... -run Ollama   # Ende-zu-Ende gegen lokales Ollama
```

Modelle für den Ende-zu-Ende-Test: `PERSONALRAG_EMBED` (Standard `bge-m3`), `PERSONALRAG_LLM` (Standard `gemma4:12b`).

## Offen

- MCP-Server (stdio-Brücke `--mcp` und Streamable HTTP); die Agenten-Ansicht speichert Freigaben bereits
- Dateisystem-Watcher (bisher Abgleich beim Start, alle 15 Minuten und auf Knopfdruck)
- OCR für Scans (werden als „Braucht OCR“ markiert), weitere Formate (.eml, .pptx, .xlsx)
- Folgefragen mit `CondenseFollowUp`, gespeicherter Verlauf, Filter nach Dateityp und Datum
- PDF-Vorschau mit pdf.js statt Textauszug
