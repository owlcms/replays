package httpServer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

type replaySentinel struct {
	Camera   int    `json:"camera"`
	Session  string `json:"session"`
	Filename string `json:"filename"`
}

var (
	replayReadyWaitTimeout  = 45 * time.Second
	replayReadyPollInterval = 100 * time.Millisecond
	replayGenerationMu      sync.Mutex
	replayGenerations       = make(map[int]int64)
)

func currentReplayGeneration(camera int) int64 {
	replayGenerationMu.Lock()
	defer replayGenerationMu.Unlock()
	return replayGenerations[camera]
}

func bumpReplayGeneration(camera int) int64 {
	replayGenerationMu.Lock()
	defer replayGenerationMu.Unlock()
	replayGenerations[camera]++
	return replayGenerations[camera]
}

func replaySentinelPath(camera int) string {
	return filepath.Join(config.GetVideoDir(), fmt.Sprintf(".latest_replay_camera_%d.json", camera))
}

func replayLogTimestamp() string {
	return time.Now().Format("2006-01-02 15:04:05.000 MST")
}

func sanitizeReplayFilename(filename string) (string, error) {
	trimmed := strings.TrimSpace(filename)
	if trimmed == "" || trimmed == "." || trimmed == ".." || strings.ContainsAny(trimmed, `/\\`) {
		return "", os.ErrInvalid
	}

	return trimmed, nil
}

// ClearReplaySentinel removes the per-camera replay pointer before a trim starts.
func ClearReplaySentinel(camera int) error {
	if camera < 1 {
		return fmt.Errorf("invalid camera number %d", camera)
	}

	sentinelPath := replaySentinelPath(camera)
	generation := bumpReplayGeneration(camera)
	logging.InfoLogger.Printf("=== REPLAY SENTINEL CLEAR timestamp=%s camera=%d generation=%d sentinel=%q ===", replayLogTimestamp(), camera, generation, sentinelPath)

	err := os.Remove(sentinelPath)
	if err == nil {
		logging.InfoLogger.Printf("=== REPLAY SENTINEL CLEARED timestamp=%s camera=%d generation=%d sentinel=%q ===", replayLogTimestamp(), camera, generation, sentinelPath)
		return nil
	}
	if os.IsNotExist(err) {
		logging.InfoLogger.Printf("=== REPLAY SENTINEL ALREADY ABSENT timestamp=%s camera=%d generation=%d sentinel=%q ===", replayLogTimestamp(), camera, generation, sentinelPath)
		return nil
	}
	return err
}

// PublishReplaySentinel atomically points /replay/{camera} at a completed replay file.
func PublishReplaySentinel(camera int, session string, filename string) error {
	if camera < 1 {
		return fmt.Errorf("invalid camera number %d", camera)
	}

	cleanSession, err := sanitizeReplaySessionID(session)
	if err != nil {
		return fmt.Errorf("invalid replay session %q: %w", session, err)
	}

	cleanFilename, err := sanitizeReplayFilename(filename)
	if err != nil {
		return fmt.Errorf("invalid replay filename %q: %w", filename, err)
	}

	parsedReplay, ok := parseReplayFilename(cleanSession, cleanFilename)
	if !ok {
		return fmt.Errorf("replay filename does not match expected format: %s", cleanFilename)
	}
	if parsedReplay.Camera != camera {
		return fmt.Errorf("replay filename camera %d does not match sentinel camera %d", parsedReplay.Camera, camera)
	}

	videoPath := filepath.Join(config.GetVideoDir(), parsedReplay.Session, parsedReplay.Filename)
	info, err := os.Stat(videoPath)
	if err != nil {
		return fmt.Errorf("completed replay file is not available: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("completed replay path is a directory: %s", videoPath)
	}
	sentinelPath := replaySentinelPath(camera)
	logging.InfoLogger.Printf("=== REPLAY SENTINEL WRITE START timestamp=%s camera=%d session=%q filename=%q video=%q sentinel=%q ===", replayLogTimestamp(), camera, parsedReplay.Session, parsedReplay.Filename, videoPath, sentinelPath)

	payload, err := json.Marshal(replaySentinel{
		Camera:   camera,
		Session:  parsedReplay.Session,
		Filename: parsedReplay.Filename,
	})
	if err != nil {
		return err
	}

	sentinelDir := config.GetVideoDir()
	if err := os.MkdirAll(sentinelDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create replay sentinel directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(sentinelDir, fmt.Sprintf(".latest_replay_camera_%d_*.tmp", camera))
	if err != nil {
		return fmt.Errorf("failed to create replay sentinel temp file: %w", err)
	}

	tmpName := tmpFile.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(append(payload, '\n')); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to write replay sentinel: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to sync replay sentinel: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close replay sentinel: %w", err)
	}

	finalPath := sentinelPath
	if err := os.Rename(tmpName, finalPath); err != nil {
		if removeErr := os.Remove(finalPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("failed to replace replay sentinel: %w", err)
		}
		if retryErr := os.Rename(tmpName, finalPath); retryErr != nil {
			return fmt.Errorf("failed to publish replay sentinel: %w", retryErr)
		}
	}

	renamed = true
	logging.InfoLogger.Printf("=== REPLAY SENTINEL WRITE COMPLETE timestamp=%s camera=%d session=%q filename=%q video=%q sentinel=%q ===", replayLogTimestamp(), camera, parsedReplay.Session, parsedReplay.Filename, videoPath, finalPath)
	return nil
}

func findPublishedReplayForCamera(camera int) (*ReplayCameraState, error) {
	if camera < 1 {
		return nil, fmt.Errorf("invalid camera number %d", camera)
	}

	contents, err := os.ReadFile(replaySentinelPath(camera))
	if err != nil {
		return nil, err
	}

	var sentinel replaySentinel
	if err := json.Unmarshal(contents, &sentinel); err != nil {
		return nil, fmt.Errorf("failed to parse replay sentinel: %w", err)
	}
	if sentinel.Camera != camera {
		return nil, fmt.Errorf("replay sentinel camera %d does not match requested camera %d", sentinel.Camera, camera)
	}

	session, err := sanitizeReplaySessionID(sentinel.Session)
	if err != nil {
		return nil, fmt.Errorf("invalid replay sentinel session %q: %w", sentinel.Session, err)
	}

	filename, err := sanitizeReplayFilename(sentinel.Filename)
	if err != nil {
		return nil, fmt.Errorf("invalid replay sentinel filename %q: %w", sentinel.Filename, err)
	}

	parsedReplay, ok := parseReplayFilename(session, filename)
	if !ok {
		return nil, fmt.Errorf("replay sentinel filename does not match expected format: %s", filename)
	}
	if parsedReplay.Camera != camera {
		return nil, fmt.Errorf("replay sentinel filename camera %d does not match requested camera %d", parsedReplay.Camera, camera)
	}

	videoPath := filepath.Join(config.GetVideoDir(), parsedReplay.Session, parsedReplay.Filename)
	info, err := os.Stat(videoPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("replay sentinel path is a directory: %s", videoPath)
	}

	return &ReplayCameraState{
		Camera:        camera,
		Available:     true,
		Session:       parsedReplay.Session,
		Filename:      parsedReplay.Filename,
		VideoPath:     parsedReplay.URL,
		AthleteName:   parsedReplay.Athlete,
		LiftType:      parsedReplay.LiftType,
		AttemptNumber: parsedReplay.AttemptNumber,
		Timestamp:     parsedReplay.Timestamp,
	}, nil
}

func waitForPublishedReplayForCamera(ctx context.Context, camera int) (*ReplayCameraState, error) {
	replay, err := findPublishedReplayForCamera(camera)
	if err == nil || !os.IsNotExist(err) {
		return replay, err
	}

	deadline := time.NewTimer(replayReadyWaitTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(replayReadyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, os.ErrNotExist
		case <-ticker.C:
			replay, err = findPublishedReplayForCamera(camera)
			if err == nil || !os.IsNotExist(err) {
				return replay, err
			}
		}
	}
}
