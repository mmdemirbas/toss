---
title: Security
order: 30
summary: Toss is for trusted local networks — no login, permissive origins, a self-signed certificate — and what each of those means.
---

> [!TLDR]
> Anyone on the LAN can read and write panes; that is the design, and the reason Toss must not face the internet.

## The model {#model}

```oku-table
{"headers":["Property","Setting","Why"],"rows":[["Authentication","None","Zero-friction sharing on a network you already trust"],["WebSocket origin check","Off","Any local browser may connect"],["CORS on the SSE endpoint","`Access-Control-Allow-Origin: *`","Same reason"],["Transport","HTTPS and WSS with a self-signed certificate under `~/.toss/certs/`","No mixed-content warnings; not CA-trusted encryption"],["Markdown","Sanitised with DOMPurify before rendering","No script from a pane"]]}
```

> [!WARNING]
> Do not expose the port on a public interface or through a tunnel. Toss has no way to tell a stranger from a device of yours.
