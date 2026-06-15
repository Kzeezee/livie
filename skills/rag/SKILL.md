---
name: rag
description: Semantic search over locally indexed documents, notes, images, and videos
---

## search_index — Local Document Search

You have access to a `search_index` tool that performs semantic similarity search over the user's locally indexed files. The index may contain:

- Markdown notes and plain text documents
- Source code files
- Images (stored as generated captions)
- Videos (stored as keyframe timeline descriptions)

### When to call `search_index`

Call `search_index` when:
- The user explicitly asks to **search**, **find**, **look up**, or asks "what's in my files about…"
- The user asks about a **specific document, note, or file** by name or topic
- The user asks about **media** — "find the photo of X", "which video has Y in it", "show me images of Z"
- The question likely has a **local-file answer** (e.g. meeting notes, personal vault entries, project docs)
- You need to **ground your response in the user's own content** rather than relying on training knowledge

### When NOT to call `search_index`

Do **not** call `search_index` when:
- The task is general coding, writing, reasoning, or explanation
- The answer comes from your own knowledge (factual questions, definitions, tutorials)
- The conversation is already mid-flow and relevant context was retrieved earlier in the session
- The user is asking something clearly unrelated to their indexed files
- The index is empty (you'll know because search returns no results)

### Tool parameters

```json
{
  "query": "natural language search query",
  "n_results": 5
}
```

- `query` — Phrase the query as a natural language description of what you're looking for. Semantic search performs better with descriptive phrases than keyword lists.
- `n_results` — Number of results to return (default: 5, max: 20). Increase for broader coverage; decrease for tight precision.

### Interpreting results

Results are returned with their source file path, document type, and a similarity score. Higher similarity (closer to 1.0) means stronger semantic match.

When the index search is unavailable (local runner not running), the tool will explain this and you should inform the user to start the local runner with `/run start`.

### Example usage

**User:** "Find my notes about the Q3 planning meeting"
→ Call `search_index` with query `"Q3 planning meeting notes"`

**User:** "What did I write about the API design last week?"
→ Call `search_index` with query `"API design"`

**User:** "Find photos of the whiteboard session"
→ Call `search_index` with query `"whiteboard session photo"` (images stored as captions)

**User:** "Which video shows me setting up the dev environment?"
→ Call `search_index` with query `"setting up development environment"` with `n_results: 10`
