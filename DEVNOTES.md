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

