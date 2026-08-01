### Project Structure

- `cmd/video`: Main application entry point (hosts the Cameras and Replays modules)
- `internal/`: Private application code
  - `cameras/`: Cameras module UI and stream management
  - `replays/`: Replays module UI, HTTP server wiring and MQTT monitoring
  - `videoconfig/`: Resolves the shared configuration directory
  - `api/`: API handlers and middleware
  - `models/`: Data models
  - `service/`: Business logic
- `pkg/`: Public packages that can be used by external projects
- `configs/`: Configuration files
- `scripts/`: Build and deployment scripts
- `test/`: Additional test files

### Running in IDE

```bash
# Run the application (both modules)
go run ./cmd/video

# Run with only one module visible
go run ./cmd/video --no-replays
go run ./cmd/video --no-cameras
```

### Configuration-Driven Code

When a feature is driven by configuration, the configuration file and its loader are the single source of truth. Do not add parallel hardcoded fallback logic in the feature code that silently hides missing or incomplete configuration. If defaults are needed, put them in the config loader or embedded default config and make load failures visible.

