# TODO

Open work on PersonalRAG, roughly by priority. Done items are removed; the history is in the pull requests.

## Agent tools

The in-app agent and the MCP server offer `list_collections`, `list_documents`, `read_document`, `search_documents` and `extract` (the in-app agent also `calc`). `extract` runs the local answer model with a
prompt that forbids inventing anything. Two kinds of task are not covered well; the demo benchmark (`demo/cases.yaml`) shows both.

- [ ] **`check`: qualitative rules and rule search.** `check(document_id, reference_document_id)` decides rules with a number or a required item (length, sources, declaration) in code; every other rule (style, language, structure) is reported as "cannot tell". Left: let the local model judge those with reason and page (risk: rules wrongly reported as violated, see the benchmark), take rules from a search (`reference_query` + `reference_collection_id`) instead of one document, and run the benchmark case `pruefung-regeln`.
- [ ] **`generate`: create content from a document with the local model**
  (exam questions, summaries, explanations). Grounded in the document, own additions marked, page references; injection protection as for `extract`. Needed above all with "document text stays on this computer", where the agent has no `read_document` and `extract` may not generate: exam questions are impossible there today. Separate cache namespace.
- [ ] Offer `generate` in the in-app agent and over MCP; describe it in the agent instructions; keep it atomic (one tool, one step).
- [ ] Lower priority: `query_document(document_id, question, pages?)`, a discursive answer about one document with page references. The answer mode with a collection in scope covers most of it.
- [ ] Later: stream long `generate` results (MCP progress notifications).
- [ ] Store agent steps with the turn, so they survive a restart.
- [ ] Make the agent count with `calc` reliably (it still counts in its head sometimes).

### Starlark (`calc`)

`calc` is the only place Starlark runs today (`internal/agent/calc.go`): sandboxed, step limit, no files or network, data through `outputs`, the document tools as functions (`list_documents`, `extract`, …), so one script can cover every document instead of one model turn each, a per-run `state` dict, `math`, `time` and the helpers `normalize_name`, `count_by` and `table`.

- [ ] Benchmark the script tools: run the agent cases of `demo/cases.yaml` with qwen3.6:27b and gemma4:12b before and after, and see whether weak models write the loop correctly; adjust `agentSystem` accordingly.
- [ ] **Files from scripts.** A script builds CSV or `.ics` and the app hands the file to the UI; covers "Reuse results" under Everyday use.

## Everyday use

What would make the app useful day to day, in order of benefit.

- [ ] **More file formats.** Today: PDF, Word (`.docx`), Markdown, text, HTML. Missing above all: e-mail (`.eml`, `.msg`, mbox exports), Excel and CSV, PowerPoint, images (photos of receipts, screenshots) through the existing OCR, OpenDocument (`.odt`) and RTF. Without them, documents have to be converted first.
- [ ] **Quick question from anywhere.** A global shortcut opens a small question window (like Spotlight) with answer and sources; the app already runs in the tray.
- [ ] **Ask one document.** "Ask about this document" in the sources view and the preview, limited to that document, with page references (the UI side of `query_document`).
- [ ] **Reuse results.** Export agent tables (attendance counts, rule checks) as CSV or Excel; turn deadlines and dates found in contracts or minutes into a calendar file (`.ics`).
- [ ] **An installable release** for other people: signed packages and an update check (see Releases); a prerequisite once others use the app.

## Benchmark and demo

- [ ] Run the agent cases with stronger agent models (glm-5.2 on GPUStack) and record the reports.
- [ ] Judge open tasks (exam questions, rule checks) beyond keyword checks, e.g. with a model as judge; flag rules wrongly reported as violated.
- [ ] Use case "personal documents": contracts, insurance, invoices (some scanned); deadlines, notice periods, cost tables.
- [ ] Check the demo and the setup wizard in the running app on all three systems (screenshots for the README).
- [ ] Check how well models follow the collection notes.

## Text extraction and search

- [ ] OCR: keep line breaks in lists; a scanned participant list comes out as one paragraph and names run together.
- [ ] askyourdocs: page-level column detection (gutters over many lines, XY-cut) for two-column articles whose lines sit on different baselines; equal-length lists side by side are taken for a table today.
- [ ] Search: synonyms rank late ("Firmenwagen" for "Dienstwagen" is missing from the first page of hits).
- [ ] OCR on Windows (Windows.Media.Ocr); Linux later.

## Releases

- [ ] First release from the workflow (tag `v0.1.0`), then publish the draft.
- [ ] Sign and notarise the macOS app (Apple Developer ID); sign the Windows installer.
- [ ] Update check, later automatic updates (Sparkle / WinSparkle) once the packages are signed.
- [ ] Move GitHub Actions to versions running on Node.js 24 (checkout, setup-go, setup-node, artifacts).
- [ ] Test on Windows hardware (M1 milestone).

## Ideas

- [ ] Expert mode: own `.prompt` files in `<data>/prompts` replace the built-in ones, shown in diagnostics, falling back on schema errors.
- [ ] A separate "reading model" for `extract`, independent of the answer model.
