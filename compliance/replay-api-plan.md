# Replay API Plan

## Goal

Add a machine-readable replay API beside the existing HTML and file-serving routes so another platform can browse sessions, retrieve replay lists within a selected session, sort by athlete, filter to a specific athlete, and still fetch the most recent completed lift.

Results should be grouped by lift, with the camera files nested under each lift, so the external platform does not need to reconstruct multi-camera groupings itself.

## Scope Decisions

Included:
- session discovery
- per-session lift browsing
- athlete sort within a session
- exact-match athlete filter within a session
- latest completed lift retrieval
- grouped results by lift, then camera
- preserve the current browser UI
- preserve the current `/replay/{camera}` endpoint
- preserve `/videos/{session}/{file}` as the raw media URL

Excluded:
- cross-session athlete search
- pagination
- authentication or authorization
- storage-format changes

Confirmed choices:
- athlete filtering is exact match only
- athlete lookup is session-scoped
- per-session lift lists return all lifts in one response
- expected scale is about 120 lifts x 4 cameras

## Implementation Steps

1. Extract the replay scanning and filename parsing logic from the existing HTML list handler and latest replay handler in `internal/httpServer/server.go` into shared helpers.
2. Reuse the filename and session conventions already established in `internal/recording/recorder.go`.
3. Define a shared replay model with:
   - session
   - timestamp
   - athlete
   - lift type
   - attempt
   - `replays[]` with camera number, filename, and media URL
4. Group files into one lift when session, timestamp, athlete, lift type, and attempt match.
5. Add `GET /api/sessions` to return available sessions and the active session marker.
6. Add `GET /api/sessions/{session}/lifts` to return grouped lifts for a session.
7. Support `sort=time` and `sort=athlete` on the session lift list.
8. Support `athlete={name}` as an exact-match filter within the selected session.
9. Add `GET /api/replays/latest` to return the latest grouped lift.
10. Reuse the existing fallback behavior for latest lift retrieval when there is no active session.
11. Keep the current HTML handler using the same shared parsing and grouping logic so browser behavior and API behavior do not drift.
12. Document the new API alongside the legacy endpoints.

## Recommended Endpoint Surface

- `GET /api/sessions`
- `GET /api/sessions/{session}/lifts`
- `GET /api/replays/latest`
- preserve `GET /videos/{session}/{file}`
- preserve `GET /replay/{camera}`

## Verification

1. `GET /api/sessions` returns available sessions and correctly marks the active one.
2. `GET /api/sessions/{session}/lifts` returns grouped lifts rather than flat camera files.
3. `GET /api/sessions/{session}/lifts?sort=athlete` orders by athlete and then by time.
4. `GET /api/sessions/{session}/lifts?athlete={name}` returns only that athlete's lifts within the selected session.
5. `GET /api/replays/latest` returns the latest grouped lift and matches current latest-file selection behavior.
6. Returned `/videos/...` URLs are directly playable.
7. Partial multi-camera lifts are returned with the available files.
8. Malformed filenames are skipped safely.
9. The existing `/` HTML page still works after the shared logic is introduced.

## Relevant Files

- `internal/httpServer/server.go`
- `internal/httpServer/status.go`
- `internal/httpServer/templates/videolist.html`
- `internal/recording/recorder.go`
- `README.md`
- `OBS.md`

## Follow-up Notes

1. If a future client needs athlete search across all sessions, add a separate cross-session search endpoint rather than overloading the session list.
2. If filename parsing proves fragile for legacy or malformed names, isolate parsing rules in a dedicated helper and define skip behavior explicitly.
3. If session sizes later grow beyond current expectations, add paging to the session lift list as a separate enhancement.
