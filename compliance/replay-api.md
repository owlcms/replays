# Replay API Specification

## Purpose

This specification defines the HTTP API needed by an external client to retrieve replay sessions, browse lifts within a session, filter by athlete, sort by athlete, and fetch the most recent completed lift.

The API is intended to replace dependence on the current HTML interface while preserving the existing raw media URLs.

## Scope

Included:
- list sessions
- list all lifts in a selected session
- exact-match athlete filtering within a session
- athlete-sorted lift listing within a session
- latest completed lift retrieval
- grouped results by lift, then by camera
- raw media playback through existing video URLs

Excluded:
- cross-session athlete search
- pagination
- authentication/authorization
- changes to file storage format
- replacement of the existing `/replay/{camera}` endpoint

## Storage Assumptions

Replay files are stored under a video root directory in per-session subdirectories.

Example layout:
- `{videoRoot}/{session}/{filename}.mp4`

Final replay filenames follow the existing convention:
- `{timestamp}_{athlete}_{liftType}_attempt{attempt}_Camera{camera}.mp4`

Example:
- `2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera2.mp4`

A single lift may have 1 to 4 camera files.

## Grouping Model

The API must group files into a single lift when these fields match:
- session
- timestamp
- athlete
- liftType
- attempt

Each grouped lift contains a `replays` array with one entry per available camera file.

If one or more camera files are missing, the lift is still returned with the available files.

## Session Identifier

The session identifier is the session folder name.

## Endpoints

### GET /api/sessions

Returns all available sessions.

Response body:
- JSON array ordered as the server chooses, or a JSON object containing a `sessions` array
- no pagination
- expected scale is about 125 sessions maximum

Recommended response:

```json
{
  "activeSession": "F1",
  "sessions": [
    {
      "id": "F1",
      "name": "F1",
      "active": true,
      "liftCount": 120
    },
    {
      "id": "F2",
      "name": "F2",
      "active": false,
      "liftCount": 98
    }
  ]
}
```

Field definitions:
- `activeSession`: current active session name if any, otherwise empty string or null
- `sessions[].id`: session folder name
- `sessions[].name`: display name for the session; may equal `id`
- `sessions[].active`: true when this is the active session
- `sessions[].liftCount`: number of grouped lifts in the session

Behavior:
- exclude `unsorted` unless there is a clear client requirement to expose it
- return `200 OK` with an empty `sessions` array when there are no sessions

### GET /api/sessions/{session}/lifts

Returns all lifts for the specified session.

Query parameters:
- `sort`: optional
  - `time` = newest first
  - `athlete` = athlete ascending, then time ascending within athlete
- `athlete`: optional exact athlete match within this session

Notes:
- all lifts are returned in one response
- expected upper bound is about 120 lifts x 4 cameras, which is acceptable for a single response
- if both `sort` and `athlete` are present, filtering is applied first, then sorting

Recommended response:

```json
{
  "session": {
    "id": "F1",
    "name": "F1",
    "active": true
  },
  "sort": "time",
  "athleteFilter": null,
  "liftCount": 2,
  "lifts": [
    {
      "timestamp": "2026-03-18_19h47m51s",
      "athlete": "YUM Lisa",
      "liftType": "SNATCH",
      "attempt": 1,
      "replayCount": 2,
      "replays": [
        {
          "camera": 1,
          "filename": "2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera1.mp4",
          "url": "/videos/F1/2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera1.mp4"
        },
        {
          "camera": 2,
          "filename": "2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera2.mp4",
          "url": "/videos/F1/2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera2.mp4"
        }
      ]
    },
    {
      "timestamp": "2026-03-18_19h44m10s",
      "athlete": "LEE Mina",
      "liftType": "SNATCH",
      "attempt": 1,
      "replayCount": 1,
      "replays": [
        {
          "camera": 1,
          "filename": "2026-03-18_19h44m10s_LEE_Mina_SNATCH_attempt1_Camera1.mp4",
          "url": "/videos/F1/2026-03-18_19h44m10s_LEE_Mina_SNATCH_attempt1_Camera1.mp4"
        }
      ]
    }
  ]
}
```

Field definitions:
- `session.id`: session folder name from the route parameter
- `session.name`: display name; may equal `id`
- `session.active`: true when this is the current session
- `sort`: effective sort mode used by the server
- `athleteFilter`: exact athlete filter used, or null when none
- `liftCount`: number of grouped lifts returned
- `lifts[].timestamp`: lift timestamp from the filename
- `lifts[].athlete`: athlete name as reconstructed from the filename
- `lifts[].liftType`: `SNATCH` or `CLEANJERK`
- `lifts[].attempt`: integer attempt number
- `lifts[].replayCount`: count of available camera files in this lift
- `lifts[].replays[].camera`: camera number
- `lifts[].replays[].filename`: raw filename
- `lifts[].replays[].url`: playable media URL served by the existing `/videos/...` handler

Behavior:
- default sort is `time`
- `sort=athlete` sorts by normalized athlete name ascending, then by timestamp ascending inside each athlete
- `athlete={name}` is an exact match against the reconstructed athlete display name
- exact match should be documented as case-sensitive or case-insensitive during implementation; recommendation is case-insensitive exact normalization if that matches current UI expectations
- return `404 Not Found` if the session does not exist
- return `200 OK` with `liftCount: 0` and an empty `lifts` array when the session exists but has no valid replay files
- malformed filenames that do not parse into lift metadata should be skipped and not break the response

### GET /api/replays/latest

Returns the most recent completed lift as a grouped lift.

Optional query parameters:
- `session`: optional session id; when omitted, use the current active session, or if none exists, fall back to the most recently modified session as the server currently does for latest replay selection

Recommended response:

```json
{
  "session": {
    "id": "F1",
    "name": "F1",
    "active": true
  },
  "lift": {
    "timestamp": "2026-03-18_19h47m51s",
    "athlete": "YUM Lisa",
    "liftType": "SNATCH",
    "attempt": 1,
    "replayCount": 2,
    "replays": [
      {
        "camera": 1,
        "filename": "2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera1.mp4",
        "url": "/videos/F1/2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera1.mp4"
      },
      {
        "camera": 2,
        "filename": "2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera2.mp4",
        "url": "/videos/F1/2026-03-18_19h47m51s_YUM_Lisa_SNATCH_attempt1_Camera2.mp4"
      }
    ]
  }
}
```

Behavior:
- returns one grouped lift, not one file
- if no active session exists, use the most recently modified session directory
- if the chosen session has no valid replay files, return `404 Not Found`
- partial multi-camera results are valid

## Existing Endpoints to Preserve

### GET /videos/{session}/{filename}

Purpose:
- raw media playback URL

Behavior:
- remains the canonical media URL returned by API responses

### GET /replay/{camera}

Purpose:
- backward compatibility for current single-camera consumers such as OBS

Behavior:
- unchanged
- not sufficient for grouped external client retrieval by itself

## Sorting and Filtering Rules

### Time sort
- default behavior
- newest lift first

### Athlete sort
- order by athlete name ascending
- for the same athlete, order by timestamp ascending
- response remains grouped by lift

### Athlete filter
- session-scoped only
- exact match only
- no substring, prefix, fuzzy, or cross-session search

## Error Handling

Recommended status codes:
- `200 OK` for successful responses, including empty session lists or empty lift lists for an existing session
- `400 Bad Request` for invalid query parameters such as unsupported `sort`
- `404 Not Found` for nonexistent sessions or when `latest` cannot resolve to any valid lift
- `500 Internal Server Error` for filesystem or parsing failures that prevent response generation

Recommended error body:

```json
{
  "error": "session not found"
}
```

## Implementation Notes

1. Refactor existing replay parsing from the HTML path into shared helpers.
2. Reuse the current latest-session fallback logic already used by the single-camera replay endpoint.
3. Reconstruct athlete names from filenames in the same way the HTML page currently does.
4. Group camera files before applying API serialization.
5. Keep HTML rendering and API responses backed by the same parsing/grouping logic to avoid behavioral drift.

## Verification Requirements

1. Session listing returns all available sessions and marks the active session correctly.
2. Per-session lift listing returns grouped lifts, not flat files.
3. Athlete exact-match filtering returns only lifts for the requested athlete within the chosen session.
4. Athlete sorting matches the agreed ordering: athlete ascending, then time ascending.
5. Latest replay retrieval returns the same newest lift that the existing single-camera endpoint would imply.
6. Media URLs returned by the API are directly playable.
7. Partial lifts with missing cameras are still returned correctly.
8. Malformed filenames are skipped safely.

## Open Implementation Choice

For `athlete` exact match, choose one normalization rule and document it clearly:
- Option A: exact case-sensitive match against the display name
- Option B: exact case-insensitive match after normalizing spaces and underscores

Recommendation:
- Option B, if it matches how users naturally search athlete names in the client.
