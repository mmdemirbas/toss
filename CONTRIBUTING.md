# Contributing to toss

## Getting Started

1. Install [Go](https://go.dev/) 1.26+ and [Task](https://taskfile.dev/).
2. Clone the repo and run the tests:

```bash
git clone https://github.com/mmdemirbas/toss.git
cd toss
task test
```

## Development Workflow

```bash
task              # Start the dev server at https://localhost:7753
task build        # Build to bin/toss
task test         # Run all tests
task quality      # Tests with race+coverage, then lint
task clean        # Remove build artifacts
```

## Submitting Changes

1. Create a branch from `main`.
2. Make your changes. Keep the scope focused — one feature or fix per PR.
3. Ensure `task quality` passes.
4. Open a pull request with a clear description.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions focused and small
- Add tests for new functionality
- Use table-driven tests where appropriate
- Avoid unnecessary abstractions — simple and direct is preferred

## Reporting Issues

Open an issue with:
- What you did (command, flags, input)
- What happened vs. what you expected
- OS, Go version (`go version`), and browser (for UI issues)
