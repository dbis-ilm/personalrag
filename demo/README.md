# Demo collections

Invented documents for typical PersonalRAG use cases. Add a folder below
`demo/` in the app to try them, or let the agent benchmark check answers
against `cases.yaml`. All names, institutions and figures are made up.

| Folder | Use case | What it exercises |
|---|---|---|
| `protokolle/` | Minutes of a sports club's committee (TSV Musterstadt 1890 e.V.), 12 sessions | Participants in a left column next to guests and the excused; headings drawn as one text object padded with spaces (even sessions); a deputy; names in the running text; protocol 47 only as a scan (needs OCR) |
| `angebote/` | 40 offer letters of an advertising agency (Blaupause Kommunikation GmbH) | Completeness: 9 offer a company car in varied wording (Dienstwagen, Firmenwagen, Dienstfahrzeug), one only on page 2; others mention look-alikes (bike leasing, public transport ticket, parking) |
| `skript/` | Lecture notes "Database Systems" in English, 6 chapters | Exam questions on chapter 3 (transactions) asked in German; a page question; two-phase locking (chapter 3) vs. two-phase commit (chapter 6) |
| `pruefung/` | Examination regulations and a writing guide (PDF), a bachelor's thesis (Word) | Check the thesis against the rules: 85 instead of at most 60 pages (visible only in its table of contents), no declaration of authorship, 12 instead of at least 20 sources; the other rules are met |

`cases.yaml` lists the questions (search, answer or agent) and the correct
results: exact attendance counts, the documents a hit list or answer must
contain, distractors that should not appear, and terms the answer must
contain.

The files are generated; do not edit them by hand:

    go run ./cmd/demogen

See [`cmd/demogen/README.md`](../cmd/demogen/README.md) for the generator.

`go test ./cmd/demogen` fails when the committed files are out of date and
checks that text extraction yields the participant and guest lists the
expectations assume.
