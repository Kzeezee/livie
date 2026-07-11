# Media Image Embedding Failure — Diagnosis & Implementation Plan

## Summary

Images point-to-index count correctly (files are walked and classified fine),
but **no image ever gets embedded into the vector store.** The root cause is
**not** the embedding endpoint and **not** the vision model's ability to see
images. The cause is that the current model is a **reasoning ("thinking")
model**, and Livie's caption request lets it burn the entire token budget on
hidden reasoning, leaving the visible caption empty. That empty caption is then
treated as a vision failure and the image is never stored.

## How the pipeline actually works

For images, Livie does **not** embed pixels. It does a two-step pipeline
(`index/indexer.go` → `indexImageFile`):

1. **Caption** the image via the chat-completions endpoint using the multimodal
   model (`index/vision.go` → `CaptionImage`).
2. **Embed the caption text** via `/v1/embeddings` and store it
   (`index/store.go` → `Add`).

So "embedding an image" depends entirely on getting a non-empty caption string
back in step 1.

## Reproduction

Server started with the exact args Livie uses (`runner/manager.go` →
`buildArgs`), model + mmproj from the live config:

```
llama-server --model Gemma-4-E2B-Uncensored-HauhauCS-Aggressive-Q6_K_P.gguf \
  --mmproj mmproj-Gemma-4-E2B-Uncensored-HauhauCS-Aggressive-f16.gguf \
  --ctx-size 16384 --flash-attn on --embeddings --pooling mean
```

Tested against `media/image2.png` (the `/media` folder actually contains
`.png`, not `.jpg`).

### Step 2 (embeddings) — WORKS

```
POST /v1/embeddings {"input":"hello world"}  →  200, 1536-dim vector
```

The embedding endpoint is healthy. The RAG store can embed text fine.

### Step 1 (caption) — reproduces the bug

Mimicking the current code (`MaxTokens: 256`, no thinking control):

```json
{
  "finish_reason": "length",
  "message": {
    "content": "",
    "reasoning_content": "Here's a thinking process to arrive at the ...
                          1. Analyze the input: The input is an image ...
                          `~/projects/livie (term-design)` ..."
  }
}
```

Key observations:
- The model **can see the image** — `reasoning_content` accurately quotes
  on-screen text from the screenshot. Vision + mmproj work perfectly.
- `content` is **empty** because all 256 tokens were consumed by
  `reasoning_content` and generation stopped on `length` before any visible
  answer was produced.

`CaptionImage` checks `resp.Choices[0].Message.Content == ""` and returns
`"vision: empty response — model may not support vision"`. `indexImageFile`
turns that into a per-file failure, so the image is counted as processed but
never embedded/stored. This matches the reported symptom exactly.

### Fix validated empirically

Disabling thinking makes the same request return a proper caption:

```json
POST /v1/chat/completions {
  "max_tokens": 512,
  "chat_template_kwargs": {"enable_thinking": false},
  ...image + prompt...
}
→ finish_reason: "stop"
  content: "This image displays a command-line interface or terminal output,
            likely from a software development environment... ✓ Claude OAuth
            ready ..."
```

Non-empty caption → step 2 embeds it → image is stored. Confirmed working.

## Root cause

`index/vision.go` `CaptionImage` was written for a plain (non-reasoning)
vision model:
- It does not disable thinking, so the model emits `reasoning_content`.
- `MaxTokens: 256` is too small to fit both a full reasoning trace *and* a
  visible caption, so the visible caption is empty.
- It reads only `Message.Content` and treats empty as "no vision support",
  masking the real cause.

The current model **is** capable of the task; the request just needs to be
shaped for a reasoning model.

## Implementation Plan

All changes are confined to `index/vision.go`. Small, low-risk.

### 1. Disable thinking for caption requests (primary fix)

Add `ChatTemplateKwargs` to both caption requests so the model emits the
answer directly. The installed `go-openai` v1.41.2 already supports this field
on `ChatCompletionRequest` (`chat_template_kwargs`).

In `CaptionImage` (and it is reused by `DescribeVideo` frames, so one change
covers both):

```go
resp, err := v.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
    Model:    v.model,
    Messages: []openai.ChatCompletionMessage{ /* unchanged */ },
    MaxTokens:          320,
    ChatTemplateKwargs: map[string]any{"enable_thinking": false},
})
```

### 2. Raise the token budget (defense in depth)

Bump `MaxTokens` from 256 → ~320. The prompt asks for 2–3 sentences; with
thinking off this is plenty, and it avoids truncation on verbose replies.
(Kept modest so video keyframe captioning stays fast.)

### 3. Robust content extraction + honest error

Make the empty-content check smarter so we (a) fall back to
`reasoning_content` if a server/template ignores the kwarg, and (b) don't
mislabel a truncation as "no vision support":

```go
choice := resp.Choices[0]
text := strings.TrimSpace(choice.Message.Content)
if text == "" {
    // Some reasoning models put the answer in reasoning_content when the
    // visible channel is empty; salvage it rather than dropping the image.
    text = strings.TrimSpace(choice.Message.ReasoningContent)
}
if text == "" {
    if choice.FinishReason == openai.FinishReasonLength {
        return "", fmt.Errorf("vision: caption truncated (raise max_tokens)")
    }
    return "", fmt.Errorf("vision: empty response — model may not support vision")
}
return text, nil
```

`ReasoningContent` and `FinishReasonLength` both exist in go-openai v1.41.2.

### 4. (Optional, follow-up) Strip stray reasoning tags

If a future template leaks `<think>...</think>` into `content`, strip that
block before returning so captions stay clean. Not required for the current
model but cheap insurance. Can be a tiny helper `stripThink(string) string`.

## Out of scope / notes

- `/media` contains `image.png` and `image2.png` (PNG), not `.jpg` as stated
  in the report. `extToMIME` already handles `.png`, so classification and MIME
  typing are fine — worth confirming the user was pointing at the right path,
  but it does not affect this fix.
- No changes needed to `runner/manager.go` (`--embeddings --pooling mean` and
  `--mmproj` are all correct), `index/embed.go`, or `index/store.go`.
- The larger `image.png` (~625 KB → ~834 KB base64) only failed in raw `curl`
  testing due to shell arg-length limits. Livie's Go client sends the body via
  the HTTP request body, so it is unaffected.

## Test plan

1. Start the local runner as usual.
2. `/index add ./media` (or the `/media` path with real images).
3. Expect: both images report success (0 failed), `chunks stored` increases by
   2, and `/index status` shows 2 vectors.
4. Query the RAG for something visible in the screenshots (e.g. "Claude OAuth"
   or "terminal") and confirm the image chunk is retrieved.
5. Regression: index a text file to confirm text chunking still embeds/stores.
