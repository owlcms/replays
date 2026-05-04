### Project Structure

- `cmd/replays`: Main application entry point
- `internal/`: Private application code
  - `api/`: API handlers and middleware
  - `models/`: Data models
  - `service/`: Business logic
- `pkg/`: Public packages that can be used by external projects
- `configs/`: Configuration files
- `scripts/`: Build and deployment scripts
- `test/`: Additional test files

### Running in IDE

```bash
# Run the application
go run cmd/replays/main.go
```

### Configuration-Driven Code

When a feature is driven by configuration, the configuration file and its loader are the single source of truth. Do not add parallel hardcoded fallback logic in the feature code that silently hides missing or incomplete configuration. If defaults are needed, put them in the config loader or embedded default config and make load failures visible.

