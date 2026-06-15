# Livie — Phase 9: RAG & Media Indexing

> **Covers:** `index/` package, chromem-go vector store, embedding via llama-server
> `/v1/embeddings`, text/code/Markdown chunking pipeline, a `search_index` AI tool,
> `/index` slash commands, and background indexing that does not block the TUI.

---

## What Was Done in Phase 8

| Area | What shipped |
|---|---|
| `session/session.go` + `store.go` | Full session persistence — save, load, list |
| `tui/screens/chat.go` | Auto-save on every `StreamDoneMsg`; `/resume` picker wired |
| `tui/components/hud.go` | Session indicator (HH:MM) on HUD row 1 right side |

---

## What This Phase Covers

From `About-Livie.md`:

> Livie includes a local media indexing pipeline using chromem-go as the vector store —
> an in-process, pure Go library with no external dependencies, no subprocess, and no
> CGo. It persists its index to disk between sessions.

> At query time, the user's message is embedded and nearest-neighbour chunks are
> retrieved. Retrieved context is injected into the prompt before the model generates
> a response. The AI can also directly invoke the vector search as a tool call.

> Indexing runs as a background process and does not block the TUI.

**In scope for Phase 9:**
- chromem-go dependency + persistent store setup
- Embedding client wrapping llama-server `/v1/embeddings`
- Text chunking for Markdown, plain text, and code files
- Background indexing goroutine with progress reporting to TUI
- `search_index` AI tool (skills/rag package)
- `/index` slash commands: `add`, `status`, `clear`

**Explicitly out of scope (future phases):**
- PDF, EPUB ingestion (require external parsing libraries)
- Image captioning via Gemma 4 vision
- Audio/video transcript ingestion (Whisper)
- Auto-inject nearest-neighbour chunks into every prompt (hybrid push/pull — Phase 10)
- Wikilink cross-reference traversal in vault files
- Web URL fetching and indexing

---

## Design Decisions

### Vector store: chromem-go

Decision already locked in `About-Livie.md`. In-process, pure Go, no CGo, persists to
disk at `~/.local/share/livie/index`. Zero external runtime dependencies.

Single collection: `"documents"`. Metadata fields on each document carry `source`,
`type` (`markdown`, `text`, `code`), `lang` (for code), and `chunk_index` so results
can be re-ordered or deduplicated at retrieval time.

### Embedding model: llama-server `/v1/embeddings`

Same model, same running process, no second binary. llama-server exposes
`/v1/embeddings` (OpenAI-compatible) when a GGUF is loaded. The go-openai client
already supports `CreateEmbeddings`. The model name sent in the request is ignored by
llama-server — it uses whatever is loaded. We send `"local"` as a placeholder.

**Implication:** embeddings are only available when the local runner is active. If the
active endpoint is remote (OpenAI, Groq, etc.), indexing and search are disabled — the
`search_index` tool returns a clear error string rather than silently failing.

### Chunking strategy

Fixed-size character chunking with overlap. No sentence-boundary detection, no semantic
splitting — those require NLP libraries or model calls. Simple, fast, deterministic.

| Document type | Chunk size | Overlap |
|---|---|---|
| Markdown / plain text | 1,000 chars | 150 chars |
| Code | 800 chars | 100 chars |

Chunk IDs: `sha256(filepath + chunk_index)[:16]` — stable across re-index of unchanged
files, allows upsert semantics in chromem-go.

File change detection: compare file `mtime` against a lightweight manifest stored
alongside the chromem-go index (`index/manifest.json`). Files whose mtime matches the
manifest entry are skipped. Deleted files are pruned from both the manifest and the
collection on the next `add` of their parent directory.

### `search_index` tool — pull model

The AI calls `search_index` explicitly when it wants to retrieve context. It is not
auto-injected into every prompt. This keeps the architecture simple and avoids the cost
of embedding every user message.

Auto-injection (every prompt embeds the user message and retrieves top-k chunks before
calling the model) is Phase 10. Phase 9 ships the pull model only.

### Background indexing

`/index add <path>` starts a background goroutine that walks the path, chunks, embeds,
and stores. The TUI is never blocked. Progress is reported back via a `tea.Cmd` ticker
that the chat screen polls. The HUD `StatusMsg` is updated while indexing is active:
`"indexing… (42/198 files)"`. On completion: `"index ready (198 files)"`.

Embedding calls are sequential (one file at a time, one chunk at a time) to avoid
overwhelming llama-server. A future phase can add a semaphore-gated worker pool.

### `/index` commands

| Command | Behaviour |
|---|---|
| `/index add <path>` | Index a file or directory recursively (background) |
| `/index status` | Show file count, chunk count, store size on disk, last updated |
| `/index clear` | Wipe the entire index and manifest |

---

## Architecture

```
/index add <path>
    │
    ▼
index.Indexer.AddPath(path)
    │
    ├─ walk files → filter by extension
    │
    ├─ manifest check → skip unchanged files
    │
    ├─ chunker.Chunk(content, chunkSize, overlap) → []Chunk
    │
    └─ for each chunk:
           embed via EmbeddingClient.Embed(text) → []float32
               └─ POST /v1/embeddings to llama-server
           collection.AddDocument(ctx, chromem.Document{...})
           manifest.Update(filepath, mtime)

AI tool call: search_index(query, n_results)
    │
    └─ EmbeddingClient.Embed(query) → []float32
       collection.Query(ctx, query, n, nil, nil) → []chromem.Result
       format results as Markdown → return to AI
```

---

## Phase Breakdown

### Phase 9A — Dependency + store setup (`index/store.go`)

Add `chromem-go` to `go.mod`:

```bash
go get github.com/philippgille/chromem-go
```

New package `index/` with `store.go`:

```go
package index

import (
    "context"
    "path/filepath"

    chromem "github.com/philippgille/chromem-go"
)

const collectionName = "documents"

// Store wraps a chromem-go persistent DB and its document collection.
type Store struct {
    db         *chromem.DB
    collection *chromem.Collection
}

// Open opens (or creates) the persistent index at indexPath.
// embedFn is called by chromem for both add and query operations.
func Open(indexPath string, embedFn chromem.EmbeddingFunc) (*Store, error) {
    db, err := chromem.NewPersistentDB(indexPath, false)
    if err != nil {
        return nil, err
    }
    col, err := db.GetOrCreateCollection(collectionName, nil, embedFn)
    if err != nil {
        return nil, err
    }
    return &Store{db: db, collection: col}, nil
}

// Add upserts a document into the collection.
func (s *Store) Add(ctx context.Context, doc chromem.Document) error {
    return s.collection.AddDocument(ctx, doc)
}

// Query returns the top n nearest documents for the given text.
func (s *Store) Query(ctx context.Context, text string, n int) ([]chromem.Result, error) {
    return s.collection.Query(ctx, text, n, nil, nil)
}

// Count returns the number of documents in the collection.
func (s *Store) Count() int {
    return s.collection.Count()
}
```

---

### Phase 9B — Embedding client (`index/embed.go`)

```go
package index

import (
    "context"
    "fmt"

    "github.com/kez/livie/config"
    openai "github.com/sashabaranov/go-openai"
)

// EmbeddingClient wraps the llama-server /v1/embeddings endpoint.
type EmbeddingClient struct {
    client *openai.Client
}

// NewEmbeddingClient creates a client pointed at the local endpoint.
// Returns an error if the active endpoint is not local — embeddings require
// llama-server; remote OpenAI endpoints do not serve our GGUF model embeddings.
func NewEmbeddingClient(cfg *config.Config) (*EmbeddingClient, error) {
    ep := cfg.ActiveEndpoint()
    oc := openai.NewClientWithConfig(openai.DefaultConfig(ep.APIKey))
    oc.BaseURL = ep.BaseURL
    return &EmbeddingClient{client: oc}, nil
}

// Embed returns the embedding vector for text.
// Model name is sent as "local" — llama-server ignores it.
func (e *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
    resp, err := e.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
        Input: []string{text},
        Model: openai.EmbeddingModel("local"),
    })
    if err != nil {
        return nil, fmt.Errorf("embed: %w", err)
    }
    if len(resp.Data) == 0 {
        return nil, fmt.Errorf("embed: empty response")
    }
    return resp.Data[0].Embedding, nil
}

// AsChromemFunc returns this client as a chromem-compatible EmbeddingFunc.
func (e *EmbeddingClient) AsChromemFunc() chromem.EmbeddingFunc {
    return func(ctx context.Context, text string) ([]float32, error) {
        return e.Embed(ctx, text)
    }
}
```

---

### Phase 9C — Chunker (`index/chunk.go`)

```go
package index

// Chunk represents a single text chunk with its origin metadata.
type Chunk struct {
    ID         string // sha256(path+index)[:16]
    Content    string
    Source     string // absolute file path
    Type       string // "markdown" | "text" | "code"
    Lang       string // e.g. "go", "python" — empty for non-code
    ChunkIndex int
}

// ChunkFile splits content into overlapping fixed-size chunks.
// Returns an empty slice for empty content.
func ChunkFile(path, content, docType, lang string, chunkSize, overlap int) []Chunk {
    // ... rune-aware sliding window
}

// ClassifyFile returns the docType and lang for the given filename,
// and whether the file should be indexed at all.
// Accepted: .md, .txt, .go, .py, .ts, .js, .rs, .sh, .yaml, .toml, .json
// Rejected: binary files, .git/, node_modules/, vendor/, etc.
func ClassifyFile(path string) (docType, lang string, ok bool) { ... }
```

---

### Phase 9D — Manifest (`index/manifest.go`)

Lightweight JSON file stored at `<indexPath>/manifest.json`.

```go
package index

// ManifestEntry records when a file was last indexed.
type ManifestEntry struct {
    Path    string `json:"path"`
    ModTime int64  `json:"mod_time"` // Unix nano
    Chunks  int    `json:"chunks"`
}

// Manifest maps absolute file paths to their last-indexed state.
type Manifest struct {
    path    string
    entries map[string]ManifestEntry
}

func LoadManifest(indexPath string) (*Manifest, error) { ... }
func (m *Manifest) NeedsIndex(path string, modTime int64) bool { ... }
func (m *Manifest) Update(entry ManifestEntry) { ... }
func (m *Manifest) Remove(path string) { ... }
func (m *Manifest) Save() error { ... }
func (m *Manifest) Stats() (fileCount, chunkCount int) { ... }
```

---

### Phase 9E — Indexer (`index/indexer.go`)

Coordinates walk → classify → chunk → embed → store. Runs in a goroutine.
Reports progress via a channel the TUI polls.

```go
package index

// Progress carries incremental indexing status back to the TUI.
type Progress struct {
    FilesTotal   int
    FilesDone    int
    CurrentFile  string
    Err          error  // non-nil = terminal error
    Done         bool   // true = indexing complete
}

// Indexer owns the Store, EmbeddingClient, and Manifest.
type Indexer struct {
    store    *Store
    embedder *EmbeddingClient
    manifest *Manifest
    progress chan Progress
}

func NewIndexer(store *Store, embedder *EmbeddingClient, manifest *Manifest) *Indexer { ... }

// AddPath starts a background goroutine to index path (file or directory).
// Progress is sent to the returned read channel. The channel is closed on completion.
func (ix *Indexer) AddPath(ctx context.Context, path string) <-chan Progress { ... }

// Status returns a human-readable status string for /index status.
func (ix *Indexer) Status(indexPath string) string { ... }

// Clear wipes the store and manifest.
func (ix *Indexer) Clear() error { ... }
```

---

### Phase 9F — `skills/rag/` skill package

New files:
```
skills/rag/SKILL.md
skills/rag/skill.go
skills/rag/tools.go
```

`search_index` tool:

```go
func searchIndexTool(ix *index.Indexer) *skills.Tool {
    return &skills.Tool{
        Name:        "search_index",
        Description: "Search the local document index using semantic similarity. Returns the top matching chunks with their source file and relevance score.",
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Natural language search query"
                },
                "n_results": {
                    "type": "integer",
                    "description": "Number of results to return (default 5, max 20)",
                    "default": 5
                }
            },
            "required": ["query"]
        }`),
        Handler: func(args string) (string, error) {
            // unmarshal → ix.store.Query → format results as Markdown table
        },
    }
}
```

`SKILL.md` instructs the AI when to call `search_index`:
- When the user asks about a document, codebase, or topic that may be in indexed files
- When memory.md doesn't have enough context and the answer might be in local files
- Explicitly when asked to "search", "find in files", "look up", etc.

---

### Phase 9G — Wire into `agent/agent.go` and `tui/`

**`agent/agent.go`:**
- Accept `*index.Indexer` in `New()` or add `SetIndexer(*index.Indexer)` method
- Register `skills/rag` builtin if indexer is non-nil

**`main.go`:**
- After `memory.Init()`, open the index store and create the indexer
- Pass indexer to `agent.New()`
- If runner not local / not running: indexer is nil (feature gracefully absent)

**`tui/commands.go`:** `/index` command registered with `add`, `status`, `clear` subcommands.

**`tui/screens/chat.go`:** Handle index progress messages:
- `IndexProgressMsg` → update `m.hud.StatusMsg` to `"indexing… (N/M files)"`
- `IndexDoneMsg` → update to `"index ready (N files)"`, revert after 5s

---

## File Map

| File | Status | Change |
|---|---|---|
| `go.mod` / `go.sum` | **Modify** | Add `github.com/philippgille/chromem-go` |
| `index/store.go` | **New** | chromem-go wrapper: Open, Add, Query, Count |
| `index/embed.go` | **New** | llama-server embedding client + chromem EmbeddingFunc adapter |
| `index/chunk.go` | **New** | Fixed-size chunker, file classifier |
| `index/manifest.go` | **New** | Lightweight mtime manifest for change detection |
| `index/indexer.go` | **New** | Walk → chunk → embed → store; progress channel |
| `skills/rag/SKILL.md` | **New** | AI guidance: when/how to call search_index |
| `skills/rag/skill.go` | **New** | Skill struct |
| `skills/rag/tools.go` | **New** | `search_index` tool handler |
| `agent/agent.go` | **Modify** | Accept + register RAG skill if indexer present |
| `main.go` | **Modify** | Init index store + indexer; pass to agent |
| `tui/commands.go` | **Modify** | `/index` command: add, status, clear |
| `tui/screens/chat.go` | **Modify** | Handle `IndexProgressMsg` / `IndexDoneMsg` → HUD status |

**No changes needed to:** `memory/`, `session/`, `runner/`, `config/`, `skills/core/`, `skills/vault/`

---

## Gating: Local Runner Required

Embedding requires llama-server. If `cfg.Endpoint.Active != "local"` or the runner is
not running, the indexer is created but the embedding client returns a clear error on
every call. The `search_index` tool returns:

```
error: index search requires the local runner (llama-server) to be running.
Start it with /run start.
```

The tool is always registered (so the AI knows it exists) but self-reports unavailability
rather than being hard-disabled. Unlike vault memory (where disabling is a user
preference), index unavailability is an environmental condition — the tool should explain
itself rather than disappear.

---

## Phase 10 Preview — Auto-Inject RAG Context

Once Phase 9 is stable, Phase 10 adds hybrid push/pull:

- Before every `StreamCmd`, embed the user message and query the index for top-k chunks
- If results exceed a relevance threshold, prepend them to the conversation as a system
  message: `"Relevant context from local files:\n\n{chunks}"`
- This happens transparently — the AI doesn't need to call `search_index` manually for
  everyday queries
- The pull model (`search_index` tool) remains for explicit targeted retrieval
- A config flag `[index] auto_inject = true/false` gates this behaviour
