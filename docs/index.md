---
title: Toss
eyebrow: Local-network sharing · Go
subtitle: Share text, code, images and files between the devices on your local network. Zero configuration.
order: 1
summary: What Toss is for, what a pane can hold, and how to start it on two devices.
---

> [!TLDR]
> One binary on each device. The first one up becomes the hub, the others find it by broadcast, and every pane — text, code, markdown, images, files — is on all of them within the second.
>
> - No accounts, no IP addresses, no config file
> - Go only; the frontend is embedded, no npm, no network at build time
> - Spokes connect outbound, so one-way firewalls do not matter

## What it looks like {#look}

![A sidebar of panes on the left, a rendered markdown checklist on the right](screenshot-markdown.png)

*A markdown pane in preview: task list, quote, a code block with its own copy button.*

## Start it {#start}

```oku-step-flow
{"steps":[{"t":"Run it on the first device","b":"`git clone https://github.com/mmdemirbas/toss.git && cd toss && task` — or `go run ./cmd/toss`. It prints the address; open `https://localhost:7753` and accept the self-signed certificate once."},{"t":"Run it on the next device","b":"Same command. It finds the hub by UDP broadcast and connects; its own page shows the same panes."},{"t":"Or just open the hub's page","b":"A phone does not need the binary: open the hub's address in the browser and paste, drop or type."}]}
```

Go 1.22+ is the only requirement, plus a LAN where UDP broadcast works.

## A pane holds {#panes}

```oku-table
{"headers":["Content","How it gets there","What you get"],"rows":[["Text and code","Type, or paste","Live highlighting with language auto-detection; pick the language in the header to override"],["Markdown","Choose *markdown*, toggle **Preview**","Rendered page; every code block gets a copy button"],["Images","`Ctrl+V` / `Cmd+V` anywhere","Shared instantly, stored under `~/.toss/files/`"],["Files","Drop anywhere, or the file chooser on a phone","Download on any device"],["The clipboard itself","*Clipboard → Tabs* or *Sync Clipboard* in the sidebar","A pane per copy, or one clipboard across devices"]]}
```

![A Go snippet in a pane, highlighted](screenshot-code.png)

## Where to go next {#next}

```oku-compare-grid
{"cards":[{"t":"How it works","b":"Hub, spokes, discovery, reverse dial, what is on disk.","href":"how-it-works.html"},{"t":"Using it","b":"Panes, tabs, shortcuts, the port flag, building.","href":"usage.html"},{"t":"Security","b":"Why there is no login, and what that means.","href":"security.html"},{"t":"Source","b":"github.com/mmdemirbas/toss — MIT.","href":"https://github.com/mmdemirbas/toss"}]}
```
