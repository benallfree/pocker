# servertiming

`Server-Timing` headers with timings for each step of a request (and each hop under pocker).

**Runs on:** Single app, or many under [pocker](../pocker/README.md).

> **Status.** Working: Go module `github.com/pockethost/pocketbase/ext/servertiming`, a plugin for [pocker](../pocker/README.md)
> (`pocker.Server{Plugins: []pocker.Plugin{servertiming.New()}}`), bundled in the `pocker` command.
> Settings: nothing; list it first so its header includes every other hop.

## License

MIT
