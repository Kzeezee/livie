# Livie — Phase 6, 7 & 8 Testing Checklist

> **Covers:** Agent streaming, session persistence, TUI wiring, HUD live data, `/resume` picker.

---

## Pre-flight

```bash
# Confirm llama-server is running and healthy
curl http://localhost:8080/health

# Start Livie
./livie
```

- [ ] HUD row 2 shows the correct model name (basename of the `.gguf` file) and `local` endpoint
- [ ] HUD token counter shows `— / — tok` or a real context size if `context_size` is set in config
- [ ] Runner chip shows `● stopped` or `● llama-server` depending on whether the runner is managing the process

---

## 1 — Basic streaming

Type a short message and press Enter:

```
Hello, who are you?
```

- [ ] The `◆ livie` header appears immediately with a `▌` cursor — no blank wait
- [ ] Text streams in chunk-by-chunk (visible incremental rendering)
- [ ] Cursor disappears once the reply finishes
- [ ] HUD token counter updates to real numbers (e.g. `142 / 16,384 tok`) sourced from the API usage report
- [ ] No `"AI backend not yet connected"` message appears

---

## 2 — Multi-turn context

Send a follow-up that requires memory of the first reply:

```
What was the first thing I asked you?
```

- [ ] The model correctly references `"Hello, who are you?"` — proves conversation history is being sent each turn

---

## 3 — Error path

Kill llama-server mid-conversation, then send a message:

```bash
# in another terminal
pkill llama-server
```

```
Tell me something.
```

- [ ] Runner chip turns red (`● error`) within ~15 seconds of the kill
- [ ] A red `✕ error` message appears: `request failed: open stream: ...`
- [ ] The streaming slot closes cleanly — no dangling `▌` cursor
- [ ] The app does not freeze; you can still type

Restart llama-server and send another message to confirm recovery.

---

## 4 — Session auto-save

After receiving at least one reply, verify the session was written to disk:

```bash
ls ~/.local/share/livie/sessions/
cat ~/.local/share/livie/sessions/*.json | head -40
```

- [ ] A file named `2026-06-...T...json` exists
- [ ] It contains your messages and the model's replies under `"messages"`
- [ ] `"preview"` is set to the first user message (≤ 72 chars)
- [ ] `"endpoint_name"`, `"model_name"`, and `"tokens_used"` are populated

---

## 5 — `/new` resets context

```
/new
```

Then immediately send:

```
What did we talk about before?
```

- [ ] The model has no memory of the previous conversation — proves `agent.Conversation().Reset()` fired
- [ ] The session file for the old conversation still exists on disk
- [ ] A new session file is created once this conversation receives its first reply

---

## 6 — Session persistence on quit

Start a new conversation with a distinctive opener:

```
Remember the phrase: BANANA REPUBLIC
```

Wait for the reply. Then quit:

- Press `ctrl+c` once → confirm prompt appears
- Press `ctrl+c` again → app exits

```bash
cat ~/.local/share/livie/sessions/*.json | grep "BANANA"
```

- [ ] The phrase appears in the session file, confirming the synchronous save-on-quit fired

---

## 7 — `/resume` picker

Relaunch `./livie`, then:

```
/resume
```

- [ ] A picker overlay appears above the input bar
- [ ] The session from step 6 is listed with its preview `"Remember the phrase: BANANA REPUBLIC"`
- [ ] Each row shows: date, time, `model @ endpoint`, preview text
- [ ] `↓` / `↑` (or `tab` / `shift+tab`) navigates rows; selected row highlights in cyan with `▶`
- [ ] `esc` dismisses the picker without loading anything
- [ ] Autocomplete does not appear simultaneously with the picker

---

## 8 — Resume loads context

Open `/resume` again and press `enter` on the BANANA REPUBLIC session:

- [ ] `"session resumed · 2026-... · N messages"` system message appears
- [ ] The full previous conversation (both sides) is visible in the viewport
- [ ] Send: `What phrase did I ask you to remember?`
- [ ] The model answers correctly — proves `LoadHistory` restored the context to the agent

---

## 9 — Context truncation _(optional — requires small context_size)_

Set a small `context_size` in `~/.config/livie/config.toml`:

```toml
[[endpoints]]
name = "local"
base_url = "http://localhost:8080/v1"
context_size = 500
```

Restart Livie and send several back-and-forth messages until:

- [ ] A system message appears: `context window ~NN% full — N older messages trimmed`
- [ ] Conversation continues normally after the warning

---

## 10 — HUD live data

```
/endpoint
```

Switch to a remote endpoint (if configured). Verify:

- [ ] HUD endpoint name updates to the new endpoint name
- [ ] HUD model name updates to `ep.Model` or `(no model)` if unset
- [ ] Runner chip disappears (hidden for non-local endpoints)
- [ ] Token max updates to `ep.context_size` or shows `— / —` if unset

---

## Quick reference

| # | Feature | Pass signal |
|---|---------|-------------|
| 1 | Live streaming | Cursor visible, text builds chunk-by-chunk |
| 2 | Multi-turn context | Follow-up correctly references prior exchange |
| 3 | Error handling | Clean error message within ~15 s; app stays alive |
| 4 | Auto-save after reply | JSON file in `~/.local/share/livie/sessions/` |
| 5 | `/new` resets agent | Model has no prior memory |
| 6 | Auto-save on quit | File updated after second `ctrl+c` |
| 7 | `/resume` picker | List renders, navigation works, `esc` dismisses |
| 8 | Resume loads context | Model recalls content from loaded session |
| 9 | Context truncation | System message when history exceeds 90% of limit |
| 10 | HUD live data | Model, endpoint, and token count update correctly |
