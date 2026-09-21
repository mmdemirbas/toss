---
title: Using it
order: 20
summary: Panes, tabs, shortcuts, the port flag, and the build commands.
---

> [!TLDR]
> **Add Tab**, or paste an image or drop a file anywhere. `Alt+W` wraps, `Escape` leaves preview. `-port` changes the port. `task build-all` cross-compiles.

## In the page {#page}

```oku-table
{"headers":["Do","How"],"rows":[["New pane","**Add Tab**, or paste an image / drop a file anywhere"],["Code with highlighting","Pick the language in the pane header, or let auto-detection choose"],["Markdown","Choose *markdown* and toggle **Preview**; every code block gets a copy button"],["Files on a phone","The file chooser on the empty pane"],["Reorder, rename, delete","Drag tabs; click the title; the `×` asks once"],["Word wrap","`Alt + W` in the editor and the preview"],["Leave preview","`Escape`"],["Hide the sidebar","The chevron beside the name; the state is remembered"]]}
```

Panes auto-title themselves from their content and keep a manual title once you set one.

## Options {#options}

```bash
./bin/toss -port 8080       # a different HTTPS port (default 7753)
```

## Build {#build}

```bash
task                # run the development server
task build          # binary for this platform → bin/
task build-all      # macOS, Windows, Linux → bin/
task test           # tests
task vendor         # re-download the vendored JS/CSS (only to bump versions)
task clean          # remove build artifacts
```

Plain Go works too: `go run ./cmd/toss`, `go build -o bin/toss ./cmd/toss`, `go test ./cmd/toss`.

```oku-table
{"headers":["Path","What"],"rows":[["`cmd/toss/`","Go source, package main — node, discovery, store, handlers, TLS, clipboard"],["`cmd/toss/web/`","Frontend (HTML/JS/CSS), embedded into the binary"],["`cmd/toss/web/vendor/`","Vendored JS/CSS/fonts, checked in"],["`Taskfile.yml`","Build commands for every platform"]]}
```
