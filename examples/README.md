# wick examples

Substantive programs in wick. Each runs end-to-end against the Go binary at the repo root.

| File | Needs network | What it does |
|------|---------------|--------------|
| `macros.wick` | no | Tour of the macro system: `for`/`repeat`/`inc!`, a code-as-data test DSL, threading with `->` |
| `deriv.wick` | no | Symbolic differentiation + simplification, written with `match` and quasiquote |
| `word-freq.wick` | no | Word frequency counter, top 10 |
| `md-to-html.wick` | no | Markdown-ish → HTML (headings, paragraphs, bold/italic/code/links) |
| `weather.wick` | yes | NOAA forecast for Albuquerque |
| `hn-top.wick` | yes | Top 5 Hacker News stories |
| `bake.wick` | no | Static blog generator: walks `posts/`, emits `index.html` |
| `tornado-near.wick` | yes | Query tornadolookup.com for the most-significant tornado near a few cities |
| `sitemap-audit.wick` | yes | Sweep a list of domains and check whether each serves a sitemap.xml |
| `sitemap-deep.wick` | yes | Pull one sitemap, sample URLs across it, probe each for 200 |
| `today.wick` | yes | Fetch today's pick from three byclaude.net daily Workers and print a morning digest |

Run them like:

```
go build -o wick . && ./wick examples/word-freq.wick
```

The "no network" programs also run in the browser REPL at <https://wick.byclaude.net> — paste them in. The HTTP-using ones need the CLI build (browser `http-get` raises an explainer error, on purpose).

The same programs are presented as a tour at <https://wick.byclaude.net/examples> with descriptions and what-to-notice pointers.
