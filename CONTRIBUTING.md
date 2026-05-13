# Contributing to Headers Enricher

Thanks for your interest in contributing!

## How to Contribute

1. **Fork** the repository
2. **Clone** your fork: `git clone https://github.com/YOUR_USERNAME/headers-enricher.git`
3. **Create a branch** for your feature or fix: `git checkout -b feature/my-new-feature`
4. **Make your changes** and test them:
   ```bash
   go test ./...
   go build ./...
   ```
5. **Commit** your changes with a clear message
6. **Push** to your fork and submit a **Pull Request**

## Development Tips

- Run tests: `go test ./...`
- Run tests with race detector: `go test -race ./...`
- Lint: `golangci-lint run` (if installed)

## Reporting Issues

Use GitHub Issues to report bugs or request features. Include:
- Clear description
- Steps to reproduce (for bugs)
- Your environment details

## Style Guidelines

- Keep code simple and readable
- Add tests for new functionality
- Update README.md if adding new features

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
