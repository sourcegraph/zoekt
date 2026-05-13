# Contributing

We welcome contributions to the project! To propose a change, please fork the repository, make your changes, then submit
a pull request. If the change is significant or potentially controversial, please open an issue first to discuss it.
Zoekt does not require a CLA to contribute.

Before opening a pull request, make sure that you have run the tests locally:
```sh
go test ./...
```

It's also good to run a local smoke test for the relevant component. For example, if you've made changes in
`zoekt-git-index`, you can try indexing a repository locally:
```sh
go run ./cmd/zoekt-git-index /path/to/repo
```

