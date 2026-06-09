---
name: core-tools
description: "Built-in file, shell, and search tools (bash, read_file, write_file, edit_file, find_files)."
---

# Core Tools

Five built-in tools for reading, writing, and searching files and running shell commands.

## Behaviour notes

- `edit_file` validates **all** edits before writing — failure is atomic (no partial writes)
- `bash` non-zero exit returns `output + [exit N]`, not a Go error — the model always sees the output
- `find_files` matches the **filename only** (not the full path); use `bash` + `find` for full-path patterns
- `read_file` accepts `offset` (1-indexed start line) and `limit` (max lines) to window large files
- `write_file` creates parent directories automatically
