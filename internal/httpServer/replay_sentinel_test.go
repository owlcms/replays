package httpServer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/state"
)

func withReplayTestVideoDir(t *testing.T) string {
	t.Helper()

	oldVideoDir := config.GetVideoDir()
	oldSession := state.CurrentSession
	oldWaitTimeout := replayReadyWaitTimeout
	oldPollInterval := replayReadyPollInterval
	replayGenerationMu.Lock()
	oldGenerations := make(map[int]int64, len(replayGenerations))
	for camera, generation := range replayGenerations {
		oldGenerations[camera] = generation
	}
	replayGenerationMu.Unlock()

	videoDir := t.TempDir()
	config.SetVideoDir(videoDir)
	state.CurrentSession = "3"
	replayReadyWaitTimeout = 20 * time.Millisecond
	replayReadyPollInterval = time.Millisecond

	t.Cleanup(func() {
		config.SetVideoDir(oldVideoDir)
		state.CurrentSession = oldSession
		replayReadyWaitTimeout = oldWaitTimeout
		replayReadyPollInterval = oldPollInterval
		replayGenerationMu.Lock()
		replayGenerations = oldGenerations
		replayGenerationMu.Unlock()
	})

	return videoDir
}

func writeReplayTestFile(t *testing.T, videoDir string, session string, filename string) {
	t.Helper()

	sessionDir := filepath.Join(videoDir, session)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, filename), []byte("video"), 0644); err != nil {
		t.Fatalf("failed to write replay file: %v", err)
	}
}

func TestReplaySentinelPublishesAndClears(t *testing.T) {
	videoDir := withReplayTestVideoDir(t)
	filename := "2026-05-08_11h09m59s_LARRIVEE_Mariane_CLEANJERK_attempt1_Camera1.mp4"
	writeReplayTestFile(t, videoDir, "3", filename)

	if _, err := findPublishedReplayForCamera(1); !os.IsNotExist(err) {
		t.Fatalf("expected no replay before sentinel publish, got %v", err)
	}

	if err := PublishReplaySentinel(1, "3", filename); err != nil {
		t.Fatalf("failed to publish replay sentinel: %v", err)
	}

	replay, err := findPublishedReplayForCamera(1)
	if err != nil {
		t.Fatalf("failed to read replay sentinel: %v", err)
	}
	if replay.VideoPath != "/videos/3/"+filename {
		t.Fatalf("unexpected replay path %q", replay.VideoPath)
	}

	if err := ClearReplaySentinel(1); err != nil {
		t.Fatalf("failed to clear replay sentinel: %v", err)
	}
	if _, err := findPublishedReplayForCamera(1); !os.IsNotExist(err) {
		t.Fatalf("expected no replay after sentinel clear, got %v", err)
	}
}

func TestHandleReplayRequiresPublishedSentinel(t *testing.T) {
	videoDir := withReplayTestVideoDir(t)
	filename := "2026-05-08_11h09m59s_LARRIVEE_Mariane_CLEANJERK_attempt1_Camera1.mp4"
	writeReplayTestFile(t, videoDir, "3", filename)

	recorder := httptest.NewRecorder()
	handleReplay(recorder, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/replay/1", nil), map[string]string{"camera": "1"}))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected missing sentinel to return 404, got %d", recorder.Code)
	}

	if err := PublishReplaySentinel(1, "3", filename); err != nil {
		t.Fatalf("failed to publish replay sentinel: %v", err)
	}

	recorder = httptest.NewRecorder()
	handleReplay(recorder, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/replay/1", nil), map[string]string{"camera": "1"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected published sentinel to return 200, got %d", recorder.Code)
	}
	if recorder.Body.String() != "video" {
		t.Fatalf("unexpected response body %q", recorder.Body.String())
	}
	if recorder.Header().Get("X-Replay-One-Shot") != "true" {
		t.Fatalf("expected replay response to be marked one-shot")
	}
	if recorder.Header().Get("Connection") != "close" {
		t.Fatalf("expected replay response to request connection close")
	}

	recorder = httptest.NewRecorder()
	handleReplay(recorder, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/replay/1", nil), map[string]string{"camera": "1"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected reusable sentinel to serve another independent request, got %d", recorder.Code)
	}
}

func TestReplayResponseRecorderAbortsWhenGenerationChanges(t *testing.T) {
	withReplayTestVideoDir(t)

	generation := currentReplayGeneration(1)
	response := httptest.NewRecorder()
	recorder := &replayResponseRecorder{
		ResponseWriter: response,
		camera:         1,
		generation:     generation,
	}

	bumpReplayGeneration(1)
	written, err := recorder.Write([]byte("video"))
	if err == nil {
		t.Fatal("expected stale replay response write to fail")
	}
	if written != 0 {
		t.Fatalf("expected no bytes to be written after generation change, got %d", written)
	}
	if !recorder.aborted {
		t.Fatal("expected recorder to mark response aborted")
	}
}
