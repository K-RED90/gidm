# gidm desktop app (planned — milestone M5)

The desktop GUI will be a [Wails v3](https://v3.wails.io) application with a
**Svelte** frontend, talking to the daemon over its Unix socket. Its UI
components are shared with the Chrome extension (`../extensions/chrome`).

When scaffolded, this becomes its own Go module and is added to a `go.work`
workspace at the repo root:

```sh
wails3 init -n gidm-desktop -t svelte   # run inside desktop/
```

Until then this directory is an intentional placeholder.
