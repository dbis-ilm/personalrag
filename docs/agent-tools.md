# Agent tools and Starlark

Reference for the tools that agents use to work with PersonalRAG's documents, and for the `calc` tool that runs Starlark scripts over them. For setting up an MCP client see [Using PersonalRAG with agents](../README.md#using-personalrag-with-agents).

Code: `internal/agent` (`agent.go` tools and MCP server, `check.go`, `loop.go` in-app agent, `calc.go` and `calc_lib.go` Starlark).

## Tutorial: two tasks with the demo collections

The demo collections (see [demo/README.md](../demo/README.md)) give you tasks whose correct answers are known, so you can see whether an agent model does them right. In the app: **Add demo collections**, wait until they are indexed, choose **Agent** as the mode and pick a model that can call tools under Settings › Models. The answers below are exact (`demo/cases.yaml`); how long a run takes and which steps the agent takes depends on the model.

### 1. Count attendance over twelve minutes

Task, in the `Protokolle` collection:

> Erstelle mir aus allen Protokollen des Vereinsausschusses eine Liste der Teilnehmerinnen und Teilnehmer (nur die Anwesenden, keine Gäste und keine Entschuldigten) und zähle, an wie vielen Sitzungen jede Person teilgenommen hat.

What the agent is told to do (the system prompt in `loop.go`): search finds passages, not every document, so it lists the twelve documents with `list_documents`, tries `extract` on one or two ("names under Anwesend, one per line") to see where the names sit, then limits the rest with `pages`, and finally counts in one `calc` script, either over `outputs` or with its own loop over `extract(...)`, cleaned with `normalize_name` and counted with `count_by` (see [`calc` and Starlark](#calc-and-starlark)). Every call is a line in the chat; **Stop** ends the run.

A correct run reports 29 people. For example Felix Neumeyer, Frank Lindner, Helga Brandt, Martin Albers and Sabine Möller attended all 12 sessions, Tim Köhler and Lea Schubert 3, Mia Rösler 2. Where models go wrong, by design of the documents:

- Guests and the excused stand in the right column next to the participants. Ten guests, e.g. Claudia Reuter, must not be counted.
- Protocol 47 is only a scan. It is read with text recognition (macOS); on other systems the document has status `ocr` in `list_documents` and a careful agent says it was not covered.
- A deputy ("i. V.") counts for himself, not for the member he stands in for.
- Counts that are one off usually mean the model counted in its head. Ask it to count with `calc`.

### 2. Check a thesis against the rules

Task, in the `Prüfung` collection:

> Prüfe die Bachelorarbeit von Jana Meier gegen die Regeln aus der Prüfungsordnung und dem Leitfaden für Abschlussarbeiten. Nenne für jede Regel, ob sie eingehalten ist, mit Begründung.

The rules are in two documents, so the agent calls `check` twice, once per rule document, with the thesis as `document_id`. In each call your local model lists the rules it can check and looks up the values in the thesis; the verdict then comes from evaluating the rule, so "85 pages" can never be "met" for a limit of 60 (see [`check`](#check)).

A correct run finds three violations:

1. 85 instead of at most 60 pages (visible only in the table of contents).
2. No declaration of authorship.
3. 12 instead of at least 20 sources.

All other rules (structure, abstracts, citation style, language, figure captions) are met. `check` reports those it cannot decide with numbers or presence as `cannot tell`; the agent should read them with `extract` or `read_document` and say what it found. A rule reported as met or violated without a value in its reason is a reason to look closer.

### 3. When it does not work

| Symptom | Cause and remedy |
| --- | --- |
| The agent stops after a few documents | A model that is too weak for tool calling. Try a larger one; a model on a server (glm-5.2) did task 1 in 1.5 minutes, a local qwen3.6:27b in 16 |
| `extract` is slow | The local model reads whole documents. Say where the information is ("on the first page") so the agent passes `pages`; a repeated call is cached |
| A script ends with `error` | The step shows the message and a backtrace; the agent sees it and usually corrects the code. "too many steps" means an endless loop |
| Documents are missing from the result | Status other than `ok` in `list_documents` (scan without text recognition, file not readable). A good answer lists them as not covered |
| Counts differ between runs | Names written differently (`- Anna Berg`, `Anna  Berg`): `normalize_name` before counting. |

## Two ways in

| | MCP server (`PersonalRAG --mcp`) | In-app agent |
| --- | --- | --- |
| Client | OpenCode, Claude Desktop, Claude Code, Cursor, … | The agent model chosen under Settings › Models |
| Collections | Only those shared under **Agents** | All collections |
| Tools | `list_collections`, `list_documents`, `read_document`, `search_documents`, `extract`, `check` | the same, plus `calc` |
| Log | Access log in the app, client name per call | Access log (client "PersonalRAG Agent") and live steps in the chat |

All tools are read-only. A document of a collection that is not shared is reported as "not found", not as forbidden, so agents learn nothing about it.

"Document text stays on this computer" (in-app agent): the agent gets no `read_document`, and `search_documents` returns where passages are (document, pages, section) without their text. Text is read only by `extract` and `check`, i.e. by the local answer model, and only their results reach the agent model.

## Tools

Ids come from `list_documents` or `search_documents`. Paths are given from the collection folder on (`Protokolle/2026/sitzung-04.pdf`).

### `list_collections`

Shared collections with id, name and document count. No parameters.

### `list_documents`

Every document of the shared collections, paged. Includes documents that could not be read (status `ocr`, `error`, `cloud`, …), so an evaluation can say what it did not cover.

| Parameter | Meaning |
| --- | --- |
| `collection_id` | Only this collection |
| `folder` | Only documents whose path contains this text |
| `format` | `PDF`, `Word`, `Markdown`, `Text` or `HTML` |
| `modified_after`, `modified_before` | `YYYY-MM-DD` |
| `offset`, `limit` | Paging; default page 200, at most 1000. `next_offset` is 0 on the last page |

Returns `documents` (`id`, `name`, `path`, `collection`, `format`, `modified`, `status`, `note`), `total`, `next_offset`.

### `search_documents`

Hybrid (semantic and keyword) search. Returns the most relevant passages, not all matches: use it to find where something is, not to cover a collection.

Parameters: `query`, `collection_id` (optional), `limit` (default 10, at most 30). Returns `passages` with `document_id`, `name`, `path`, `location` (pages and section), `score` and `text` (empty with text kept local), and `injection_warnings` when a passage contains instructions to the reader.

### `read_document`

The text of one document, paged by characters, with `[Page n]` markers for paged formats. Not available with "Document text stays on this computer".

Parameters: `document_id`, `pages` (e.g. `1-2`, `1,5-7`, `3-`; PDF only), `offset`, `max_chars` (default 20 000, at most 60 000). Returns `text`, `next_offset` (0 at the end), `total_chars`, `note`, `injection_warnings`.

### `extract`

Applies an instruction to one document with the user's local model and returns only the result per part. Document text stays with that model.

| Parameter | Meaning |
| --- | --- |
| `document_id`, `instruction` | What to extract and where it is, e.g. "list the names under Anwesend, one per line" |
| `pages` | Only these pages: much faster if you know where the information is |
| `stop_after_match` | End at the first part with a result |

Long documents are cut into parts of about 12 000 characters; the result has one entry per part (`part`, `of`, `result`, `cached`). A part without a match answers `(nothing found)`. Results are cached per model, instruction and part text, so a repeated call is free. A part that has been sent to the model is finished and cached even if the client gives up.

### `check`

Checks one document against the rules of another, e.g. a thesis against examination regulations. Hybrid: the model reads, code decides.

| Parameter | Meaning |
| --- | --- |
| `document_id` | The document to check |
| `reference_document_id` | The document with the rules. Rules in several documents: call `check` once per rule document |
| `instruction` | Which rules count (default: all checkable ones) |
| `pages`, `reference_pages` | Limit either document to pages |

Steps:

1. **Rules.** The local model reads the rule document part by part and lists the rules that can be checked, each as `{rule, where, kind, variable, question, condition}`: `kind` is `number` (a limit on pages, sources, words, …), `presence` (something must be there, or must not) or `other`; `condition` is an expression over `variable` that is true when the rule is met, e.g. `pages <= 60`.
2. **Facts.** The model looks for the answers to the questions in the checked document, part by part, with the page where it found them.
3. **Verdict.** The condition is evaluated as Starlark with the value found. The model never says "met" or "violated".

Each result has `rule`, `where`, `verdict`, `reason` and `page`:

| Verdict | When |
| --- | --- |
| `met` | The condition is true for every value found |
| `violated` | The condition is false for every value found; or a required item (`presence`) was found in no part |
| `cannot tell` | `other` rules (style, language, structure); no value found for a number; values that disagree; a condition that cannot be evaluated |

The reason names the value, page and condition, e.g. `pages: 85 (page 2); condition: pages <= 60`. `cannot tell` is not "met": read those rules yourself with `extract` or `read_document`. The reply also counts `met`, `violated` and `cannot_tell`. Intermediate model answers are cached like `extract` results.

Limits: one variable per rule; a presence rule is "violated" when the model finds nothing, so a wrong question can cause a false violation (the `reason` says "not found in any part of the document").

## `calc` and Starlark

`calc` (in-app agent only) runs [Starlark](https://github.com/google/starlark-go), a Python dialect, so that counts, sums, sorting and tables are exact instead of computed by the model. Parameter: `code`. It returns `result` (the value of the variable `result`), `output` (what `print()` wrote) and `error` (with a backtrace, so the model can fix its code).

### Sandbox

- No files, network, clock or imports; `time.now()` does not exist.
- At most 10 million execution steps (an endless loop ends with "too many steps"). The step limit does not count time spent in the document tools; the run's 30 minute limit and the Stop button do.
- Python subset with `while`, recursion, sets and top-level `for`/`if` enabled; strings, lists, dicts, comprehensions, `sorted`, `len`, `lambda`.

### What a script sees

| Name | What |
| --- | --- |
| `outputs` | Read-only list of all earlier tool results of the run, in call order; each a dict `{tool, input, document, text}`. For `extract`: the lines found (parts without a match left out); for `read_document`: the text; otherwise the result as JSON. Includes the calls made inside scripts |
| `state` | A dict that lasts for the whole run, from one `calc` call to the next |
| `list_collections`, `list_documents`, `search_documents`, `extract`, `check` | The tools as functions (see below) |
| `read_document` | Likewise, unless the text is kept local |
| `json` | `json.encode`, `json.decode` |
| `math` | Starlark's math module (`floor`, `sqrt`, …) |
| `time` | Starlark's time module without `now`: `parse_time`, `parse_duration`, `from_timestamp`, … |
| `normalize_name(line)` | Strips bullets (`-`, `*`, `•`), list numbers (`1.`), extra spaces and trailing `,;:` from a line |
| `count_by(items, key=None)` | Dict from item (or `key(item)`) to its count, highest count first, then by key |
| `table(rows, columns=None, format="markdown")` | Text table from rows that are dicts (columns default to the keys, in order) or lists (then `columns` is needed); `format` is `"markdown"` or `"csv"` |

### The tools as functions

A script calls the tools directly, with the keyword arguments of the tool, so one script can go over every document instead of one tool call each:

```python
names = []
docs = list_documents(collection_id="protokolle", format="PDF")["documents"]
for d in docs:
    r = extract(document_id=d["id"], instruction="Names under Anwesend, one per line", pages="1")
    if "error" in r:
        print("not read:", d["name"], r["error"])
        continue
    for part in r["parts"]:
        for line in part["result"].splitlines():
            name = normalize_name(line)
            if name and name != "(nothing found)":
                names.append(name)
result = count_by(names)
```

- Arguments are keyword-only and named like the tool's parameters; an unknown name is an error that ends the script.
- A result is the tool's reply as a dict (`list_documents(...)["documents"]`, `extract(...)["parts"]`).
- A call that fails, e.g. a scan that cannot be read, returns `{"error": "..."}` and the script goes on. Check for `"error"`.
- Calls run one after another. The user's model handles only a few at a time, so there is no parallelism.
- Each call is logged like a call by the model (access log, live step in the chat) and its result lands in `outputs`. The sharing rules, the local-text mode and the per-call limits apply unchanged.
- A script that reads many documents can run for minutes; Stop ends it.

### `state` across calls

```python
# call 1
state["rows"] = [{"name": d["name"], "pages": 3} for d in docs]
# call 2
result = table(state["rows"], format="markdown")
```

- `state` belongs to one run and is empty at the start of the next.
- When a script ends, Starlark freezes the values its variables hold. A value stored in `state` that the script also held in a variable (`rows = [...]; state["rows"] = rows`) is then read-only in later calls; one stored directly (`state["rows"] = [...]`) stays changeable. To be safe, replace instead of changing: `state["rows"] = state["rows"] + [new_row]`.
- Functions defined in a script can be stored in `state` and called later.
- One `calc` call at a time uses `state`.

### Counting from `outputs`

The older style, without calling tools from the script: the model calls `extract` per document and then `calc` once.

```python
names = []
for o in outputs:
    if o["tool"] == "extract":
        names += [normalize_name(line) for line in o["text"].splitlines()]
result = count_by([n for n in names if n])
```

## Guidance the agent gets

The in-app agent's system prompt (`agentSystem` in `loop.go`) and the MCP server's instructions (`instructions` in `agent.go`) tell the model: for "all", counts and tables cover every document and count in code; try an extraction on one or two documents first and limit the rest with `pages` or `stop_after_match`; clean results before counting (`normalize_name`); for many documents write one `calc` script with a loop; use `check` for rules and read what it reports as "cannot tell"; name the documents behind an answer and say what could not be read. Changing a tool's behaviour usually means changing this text, too.

## Safety

- Document text is untrusted: it is sanitised and marked as quoted material, and results carry `injection_warnings` when a text addresses the reader.
- Starlark conditions in `check` come from the model, so they are evaluated in the same kind of sandbox: no side effects, a step limit of 100 000, a failing condition is "cannot tell".
- With a model on another server, the task, document lists and everything the agent reads (`search_documents` text, `read_document`, `extract`/`check` results) go there; `extract` and `check` read the text with the local answer model.

## Adding a tool

1. Write the function `func (s *Server) name(ctx, In) (Out, outcome, error)` in `internal/agent`; get documents through `sharedDocument` so sharing applies, text through `documentText`, and model calls through `modelPart` (cache, timeout).
2. Register it in `MCP()` (`agent.go`) and in `Tools()` (`loop.go`), and for scripts in `scriptToolNames` and `scriptTools` (`calc.go`).
3. Describe it in the tool description, in `instructions` and `agentSystem` if the model needs guidance, in the UI labels (`frontend/src/i18n`, `AgentSteps.tsx`) and in the README table; update the tool list in `mcp_stdio_test.go`.
4. Test it with the fixture in `agent_test.go`; a fake model keeps the test fast.
