# Camera Reboot Stability Specification

## Purpose

This specification defines how the cameras application must preserve source identity and per-source settings across process restarts, machine reboots, and repeated device discovery.

The goal is that an operator's saved configuration continues to apply to the same physical attachment location after reboot without relying on camera display names alone.

In other words, the system must map:
- what is plugged into port A -> the same configured streaming output
- what is plugged into port B -> the same configured streaming output

The primary requirement is port/location stability, not individual camera-unit identity.

## Problem Statement

The current implementation persists USB camera settings by `matchKey` in `deviceAssignment` entries.

Today:
- Linux uses a best-effort stable identity derived from `/dev/v4l/by-path`, parsed USB location text, or `/dev/v4l/by-id`.
- Windows uses only the DirectShow camera name as the persistent key.

This is insufficient for reboot stability on Windows and is weaker than desired on Linux.

In particular, camera names are not a safe unique identifier when:
- two identical cameras are connected
- device order changes after reboot
- the OS enumerates devices in a different order
- identical devices are attached on different USB ports

## Core Requirement

Both DirectShow on Windows and V4L2 on Linux provide platform-native monikers that can be used to identify the attachment path of a local camera source.

The cameras application must use those monikers, or a stable identifier derived directly from them, as the primary persistent identity for local camera sources.

The persistent identity must distinguish:
- two identical devices connected at the same time
- the same model connected on two different USB ports
- device enumeration order changes across reboot

The persistent identity does not need to distinguish two interchangeable identical camera bodies if they are swapped between the same two configured ports. In that case, the mapping must follow the port, not the specific unit.

## Scope

Included:
- reboot stability for local camera sources
- stable mapping of saved source settings to physical devices
- identity persistence for identical devices on different ports
- operator-visible identity text in the Configuration page
- matching rules when a saved device is missing or replaced

Excluded:
- RTSP source identity changes
- hardware hotplug event handling while the app is already running
- changes to encoder selection policy
- changes to stream content or recording format

## Definitions

- `persistent identity`: the stored identifier used to match a detected camera to a saved configuration entry
- `display identity`: the operator-visible text shown in the Configuration page for recognition/debugging
- `moniker`: the platform-native stable device identifier exposed by the underlying camera subsystem
- `device assignment`: the saved per-camera configuration block keyed by persistent identity
- `attachment identity`: the identity of the port/topology location where a camera is connected

## Configuration That Must Survive Reboot

For a matched local camera source, the following settings must continue to apply after restart or reboot:
- custom source name
- short ID
- output port
- disabled state
- preferred pixel format

These values are currently stored in `deviceAssignment` entries and must remain keyed by persistent identity.

## Identity Model

Each detected local camera must expose two fields:
- `attachmentPath`: the platform-derived attachment-location identity used as the primary stable input for persistence
- `matchKey`: machine-oriented persistent identity used for config lookup
- `identity`: human-readable identity text shown in the UI

Requirements:
- `attachmentPath` must be derived from the platform moniker for the physical attachment location
- `attachmentPath` must be the primary source for persistent local-camera matching when available
- `matchKey` must be derived from the platform moniker, not from the display name alone
- `matchKey` must be stable across app restart and OS reboot when the attachment path is unchanged
- `matchKey` must be unique among simultaneously connected cameras
- `identity` should be understandable by an operator and should help distinguish attachment locations, especially when otherwise identical cameras are used
- for local cameras, attachment-path stability takes precedence over per-device serial-style identity

Recommended program model:
- `attachmentPath`: explicit field stored and propagated through detection, inventory, and config matching
- `matchKey`: compatibility key retained for legacy config migration and non-attachment fallback cases
- `identity`: operator-facing display string

## Windows Requirements

### Source of Truth

For DirectShow cameras, the app must obtain a stable device moniker from the DirectShow enumeration data rather than using only the friendly camera name.

The preferred source is the `Alternative name` value reported by ffmpeg in DirectShow device enumeration output from:
- `ffmpeg -f dshow -list_devices true -i dummy`

That `Alternative name` string is the Windows-side moniker that must be used, or normalized, for persistent identity.

The friendly display name and the `Alternative name` must be captured together during enumeration so the app can:
- show the friendly name in the UI
- store the alternative name as the persistent identity

### Required Behavior

- The persisted `matchKey` for Windows local cameras must be based on the DirectShow `Alternative name` moniker.
- Two identical cameras with the same marketed name but connected simultaneously must produce different `matchKey` values.
- Rebooting Windows must not cause saved assignments to swap between two identical cameras.
- The display identity should include enough information to distinguish otherwise identical ports/attachment locations, using the moniker or a readable moniker-derived form when needed.

### Disallowed Behavior

- Do not use only `dshow:<lowercased camera name>` as the persistent identity.
- Do not rely on detection order or list position.

## Linux Requirements

### Source of Truth

For V4L2 cameras, the app must use a platform-native moniker or canonical stable path for the device.

When enumerating cameras on Linux, the implementation must probe the special V4L naming directories that expose unique canonical names for device nodes.

At minimum, the implementation must inspect:
- `/dev/v4l/by-path`
- `/dev/v4l/by-id`

Primary choice:
1. canonical V4L2/udev path-based moniker from `/dev/v4l/by-path`

Secondary data:
2. canonical device-instance identifier from `/dev/v4l/by-id` for diagnostics, display, or fallback when `by-path` is unavailable
3. other platform-native moniker data if it is stable across reboot

### Required Behavior

- Linux enumeration must correlate each discovered `/dev/videoN` node with one of the unique names from the V4L special directories whenever possible.
- The persisted `matchKey` for Linux local cameras must come from `by-path` when available, not `/dev/videoN`.
- `/dev/videoN` must never be treated as stable identity.
- Two identical cameras on different USB ports must produce different `matchKey` values.
- Rebooting Linux must not cause assignments to move between two identical cameras if they remain attached to the same ports.
- Swapping two identical camera bodies between those ports is allowed to swap the physical units, but must not swap the configured streaming-port mapping for the ports themselves.

## Matching Rules

On startup, detected local cameras must be matched to saved `deviceAssignment` entries by `matchKey`.

Rules:
- exact `matchKey` match wins
- camera name is not a matching key
- detection order is not a matching key
- if no `matchKey` match exists, the camera is treated as a new source
- if a saved `matchKey` is not currently detected, the saved assignment remains in config but is not applied to another camera

## UI Requirements

The Configuration page must show a stable-identity string for each local source.

This string should:
- be derived from the same moniker family as `matchKey`
- help the operator distinguish identical devices
- remain readable enough to support troubleshooting

The UI may display a shortened or normalized identity string, but the stored `matchKey` must preserve the exact persistent identifier needed for matching.

## Behavior Across Reboot Scenarios

### Expected Stable Case

If a camera remains connected at the same USB path across reboot:
- the app must rediscover it with the same `matchKey`
- the same saved name, short ID, output port, disabled state, and preferred format must be applied

### Identical Cameras on Different Ports

If two identical cameras are connected to two different ports:
- each must retain its own `matchKey`
- their assignments must not swap after reboot

If those two identical cameras are physically swapped between the two ports, the assignment may follow the ports rather than the specific camera bodies.

Example acceptable behavior:
- the wide-angle camera always connected to hub port A keeps the same streaming port
- whatever identical zoom camera is connected to hub port B keeps the streaming port assigned to B
- whatever identical zoom camera is connected to hub port C keeps the streaming port assigned to C

### Camera Moved to a Different Port

If a camera is physically moved to a different USB port, it should be treated as a different source because the attachment path changed.

This is expected and acceptable because the system is intended to preserve port-based mapping, not device-body mapping.

## Failure Handling

If the app cannot obtain a platform-native moniker for a local camera:
- it may fall back temporarily to a weaker identifier
- the source must be marked internally as weakly identified
- the fallback must not silently claim reboot stability guarantees that it cannot provide

Recommended behavior:
- log a warning that a weak identity was used
- expose enough information in the UI or logs to diagnose the issue

## Migration Requirements

Existing configs may already contain weak `matchKey` values.

The implementation must define a migration strategy for those entries.

Minimum requirements:
- do not discard existing assignments automatically
- if an old weak key can be matched confidently to a newly detected moniker-based source, migrate it
- if confident migration is not possible, prefer leaving the assignment unmatched over attaching it to the wrong camera

## Acceptance Criteria

1. A single camera retains its saved name, short ID, output port, disabled state, and preferred format after app restart.
2. A single camera retains those settings after OS reboot.
3. Two identical cameras connected simultaneously receive distinct persistent identities.
4. Two identical cameras on different USB ports do not swap assignments after OS reboot.
5. If two identical cameras are swapped between two configured ports, the assignments continue to follow the ports rather than the camera bodies.
6. Local camera matching does not depend on device list order.
7. Local camera matching does not depend solely on camera display name.
8. Linux matching does not use `/dev/videoN` as persistent identity.
9. Windows matching does not use only the ffmpeg/DirectShow display name as persistent identity.
10. When a previously configured camera is absent, its saved assignment is not incorrectly applied to another camera.
11. The Configuration page shows a useful stable-identity string for operator review.

## Implementation Notes

Current code locations relevant to this spec:
- `internal/recording/autodetect.go`
- `cmd/cameras/source_inventory.go`
- `internal/config/cameras/cameras.go`

Current known gap:
- Windows currently persists local cameras using a name-derived key, which does not satisfy this specification.

Observed DirectShow enumeration behavior:
- ffmpeg `-f dshow -list_devices true -i dummy` emits a friendly video-device name line followed by an `Alternative name` line.
- The `Alternative name` line is the expected Windows moniker source for persistent identity.

Linux enumeration note:
- the implementation may still start from `v4l2-ctl --list-devices`, but it must resolve each discovered node through the V4L special-name directories to obtain the stable unique name used for persistence.

## Open Questions

1. What exact normalization rules should be applied to the Windows `Alternative name` before storing it as `matchKey`, if any?
2. How should weak-identity fallback be surfaced to the operator: log only, UI warning, or both?