# demogen

Generates the demo collections and the benchmark cases in `demo/`: invented documents for typical PersonalRAG use cases, plus `demo/cases.yaml` with the questions and what a correct answer contains.

```sh
go run ./cmd/demogen              # writes demo/
go run ./cmd/demogen -out DIR     # writes somewhere else
go test ./cmd/demogen
```

Do not edit `demo/` by hand; change the generator and run it again. The output is deterministic (fixed random seeds, fixed PDF dates), so the committed files only change when the generator does.

## Where the output goes

- **The app.** `demo/` is embedded (`demo.go`). "Add demo collections" unpacks it into `<data>/demo` and indexes it; the example questions shown in the app come from `cases.yaml`.
- **The benchmark.** `internal/agent/demo_bench_test.go` runs the cases against the collections (opt-in, needs Ollama). How to start it and which environment variables it reads is described at the top of that file.

## Collections

| Folder | Source file | Use case |
|---|---|---|
| `protokolle/` | `protocols.go` | Twelve committee minutes (PDF) with the layout that broke extraction: participants in a left column next to guests and the excused, headings drawn as one padded text object, a deputy (`i. V.`), names in running text, one session only as a scan (needs OCR). Case: count attendance per person. |
| `angebote/` | `offers.go` | 40 offer letters (PDF); nine mention a company car in varied wording (`Dienstwagen`, `Firmenwagen`, `Dienstfahrzeug`), one only on page 2; others mention look-alikes. Case: completeness. |
| `skript/` | `script.go` | English lecture notes, six chapters; questions are asked in German. Case: exam questions on one chapter, a page question, two-phase locking vs. two-phase commit. |
| `pruefung/` | `rules.go` | Examination regulations and a writing guide (PDF) and a bachelor's thesis (Word, written as a raw `.docx` zip). Three rules are broken, the others are met. Case: check the thesis against the rules. |

`layout.go` is shared: it lays a document out once as positioned text runs and renders it either as a PDF with a text layer or as a scan (one image per page, no text layer, for the OCR case). Each run is its own text object, so columns side by side are separate objects as in real office PDFs.

`main.go` holds the `cases.yaml` format (`Case`, `Expect`) and `buildCases`, which derives the expected results (for example the attendance counts) from the same data that produced the documents, so documents and expectations cannot drift apart.

## Expectations in `cases.yaml`

Each case has a `mode` (`search`, `answer` or `agent`), a `folder` and a `question`. `expect` can contain:

| Field | Meaning |
|---|---|
| `documents` | All must be among the hits or cited sources |
| `distractors` | Should not be returned; reported, not required |
| `contains`, `excludes` | Case-insensitive substrings the answer must or must not contain |
| `counts`, `not_counted` | Exact counts per person; people (guests) who must not be counted |
| `concepts`, `min_concepts` | Groups of alternative terms (any language); at least `min_concepts` groups must occur |
| `min_questions` | Question marks the answer must contain (exam questions) |

## Tests

`main_test.go` checks that:

- `demo/` is up to date (`TestCorpusUpToDate`; `README.md` in `demo/` is ignored);
- text extraction yields what the expectations assume: participant and guest lists, the offers, the script, the thesis (`Test…Extract`);
- the scanned protocol really has no text layer and needs OCR (`TestScanNeedsOCR`).

## Adding a use case

1. Add a `<name>.go` with the data (invented, fixed seed) and a `write…` function.
2. Create and write its folder in `generate` (`main.go`), and add it to the list of removed and recreated folders.
3. Add its cases to `buildCases`.
4. Add the folder to `demoSets` in `demo.go` so the app offers it, and a row to `demo/README.md`.
5. Run `go run ./cmd/demogen`, add an extraction test, and run `go test ./cmd/demogen`.
