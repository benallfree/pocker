# servertiming

`Server-Timing` headers with timings for each step of a request (and each hop under pocker).

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **Status.** Working: Go module `github.com/pockethost/pocketone/ext/servertiming`, a plugin for [pocker](../pocker/README.md)
> (`pocker.Provider{Plugins: []pocker.Plugin{servertiming.New()}}`); the stock `pocketone` binary runs it under
> `provider: pocker`.
> Settings: nothing; list it first so its header includes every other hop.

## License

MIT
