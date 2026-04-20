package httpServer

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/config/replays"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/state"
)

var (
	Server    *http.Server
	templates *template.Template
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	clients   = make(map[*websocket.Conn]bool)
	broadcast = make(chan StatusMessage)
	mu        sync.Mutex
)

type VideoInfo struct {
	Filename    string
	DisplayName string
}

type TemplateData struct {
	Videos               []VideoInfo
	StatusMsg            string
	StatusCode           StatusCode // Change type to StatusCode instead of int
	Sessions             []string
	SelectedSession      string // Currently selected directory
	ActiveSession        string // Current competition session from state
	NoSessions           bool
	Platform             string // Add Platform field
	HasMultiplePlatforms bool
	SortByAthlete        bool // Add field for athlete sorting option
	ShowAll              bool // Add field for showing all videos
	TotalCount           int  // Add field for total video count
}

type VideoCountMessage struct {
	Count int `json:"count"`
}

type ReplayCameraState struct {
	Camera        int    `json:"camera"`
	Available     bool   `json:"available"`
	Session       string `json:"session,omitempty"`
	Filename      string `json:"filename,omitempty"`
	VideoPath     string `json:"videoPath,omitempty"`
	AthleteName   string `json:"athleteName,omitempty"`
	LiftType      string `json:"liftType,omitempty"`
	AttemptNumber int    `json:"attemptNumber,omitempty"`
	Timestamp     string `json:"timestamp,omitempty"`
}

type ReplayStateResponse struct {
	ActiveSession      string              `json:"activeSession,omitempty"`
	ResolvedSession    string              `json:"resolvedSession,omitempty"`
	CurrentAthlete     string              `json:"currentAthlete,omitempty"`
	CurrentLiftType    string              `json:"currentLiftType,omitempty"`
	CurrentAttempt     int                 `json:"currentAttempt,omitempty"`
	CurrentCamera      int                 `json:"currentCamera,omitempty"`
	HasMultiplePlatform bool               `json:"hasMultiplePlatform"`
	Cameras            []ReplayCameraState `json:"cameras"`
}

type ReplayFileEntry struct {
	Camera   int    `json:"camera"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

type ReplayLift struct {
	Timestamp   string            `json:"timestamp"`
	Athlete     string            `json:"athlete"`
	LiftType    string            `json:"liftType"`
	Attempt     int               `json:"attempt"`
	ReplayCount int               `json:"replayCount"`
	Replays     []ReplayFileEntry `json:"replays"`
}

type ReplaySessionSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	LiftCount int    `json:"liftCount"`
}

type ReplaySessionsResponse struct {
	ActiveSession string                 `json:"activeSession,omitempty"`
	Sessions      []ReplaySessionSummary `json:"sessions"`
}

type ReplaySessionInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type ReplaySessionLiftsResponse struct {
	Session       ReplaySessionInfo `json:"session"`
	Sort          string            `json:"sort"`
	AthleteFilter *string           `json:"athleteFilter"`
	LiftCount     int               `json:"liftCount"`
	Lifts         []ReplayLift      `json:"lifts"`
}

type ParsedReplayFile struct {
	Session       string
	Filename      string
	Timestamp     string
	Athlete       string
	LiftType      string
	AttemptNumber int
	Camera        int
	URL           string
}

var replayFilenamePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{2}h\d{2}m\d{2}s)_(.+)_(CLEANJERK|SNATCH)_attempt(\d+)_Camera(\d+)\.mp4$`)

func init() {
	// Load templates from embedded filesystem
	var err error
	templates, err = template.ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		panic(err)
	}
}

// StartServer starts the HTTP server on the specified port
func StartServer(port int, _ bool) {
	router := mux.NewRouter()

	// Serve static files from embedded filesystem
	fileServer := http.FileServer(getFileSystem())
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fileServer))

	// Serve video files
	x := config.GetVideoDir()
	logging.InfoLogger.Printf("Serving video files from %s\n", x)
	router.PathPrefix("/videos/").Handler(http.StripPrefix("/videos/", http.FileServer(http.Dir(x))))

	router.HandleFunc("/", listFilesHandler)
	router.HandleFunc("/api/sessions", handleReplaySessions)
	router.HandleFunc("/api/sessions/{session}/lifts", handleReplaySessionLifts)
	router.HandleFunc("/api/replay-state", handleReplayState)
	router.HandleFunc("/ws", handleWebSocket)
	// Accept /replay/{camera:[0-9]+} and /replay/{camera:[0-9]+}.mp4
	router.HandleFunc("/replay/{camera:[0-9]+}", handleReplay)
	router.HandleFunc("/replay/{camera:[0-9]+}.mp4", handleReplay).Name("replay-mp4")

	addr := fmt.Sprintf(":%d", port)
	Server = &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// Start the WebSocket broadcaster
	go handleMessages()

	logging.InfoLogger.Printf("Starting HTTP server on %s\n", addr)
	if err := Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logging.ErrorLogger.Printf("Failed to start server: %v", err)
	}
}

// listFilesHandler lists all files in the videos directory as clickable hyperlinks
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	files, err := os.ReadDir(config.GetVideoDir())
	if err != nil {
		http.Error(w, "Failed to read videos directory", http.StatusInternalServerError)
		return
	}

	// Get selected session from query parameter or active session
	selectedSession := r.URL.Query().Get("session")
	if selectedSession == "" {
		selectedSession = strings.ReplaceAll(state.CurrentSession, " ", "_")
	}

	// Get sorting preference from query parameter
	sortByAthlete := r.URL.Query().Get("sortBy") == "athlete"

	// Get time ordering preference (asc/desc) for athlete sorting
	timeOrder := r.URL.Query().Get("timeOrder")
	ascendingTime := timeOrder == "asc"

	// Get showAll preference from query parameter
	showAll := r.URL.Query().Get("showAll") == "true"

	// Get list of sessions (subdirectories)
	var sessions []string
	for _, f := range files {
		if f.IsDir() && f.Name() != "unsorted" {
			sessions = append(sessions, f.Name())
		}
	}

	// If no sessions exist, show a message instead
	if len(sessions) == 0 {
		data := TemplateData{
			NoSessions: true,
		}
		templates.ExecuteTemplate(w, "videolist.html", data)
		return
	}

	// Create directory if it doesn't exist yet
	sessionDir := filepath.Join(config.GetVideoDir(), selectedSession)
	if selectedSession != "" && selectedSession != "unsorted" {
		if err := os.MkdirAll(sessionDir, os.ModePerm); err != nil {
			logging.ErrorLogger.Printf("Failed to create session directory: %v", err)
		}
	}

	// Read files from the session directory
	sessionDir = filepath.Join(config.GetVideoDir(), selectedSession)
	files, err = os.ReadDir(sessionDir)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "Failed to read session directory", http.StatusInternalServerError)
		return
	}

	// Sort files in reverse order (most recent first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() > files[j].Name()
	})

	// Count only valid video files (those starting with a date)
	datePattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	validFiles := make([]os.DirEntry, 0)
	for _, file := range files {
		if !file.IsDir() && datePattern.MatchString(file.Name()) {
			validFiles = append(validFiles, file)
		}
	}

	// Regex to extract date, hour, name, lift type, attempt, and camera
	re := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{2}h\d{2}m\d{2}s)_(.+)_(CLEANJERK|SNATCH)_attempt(\d+)_Camera(\d+)\.mp4$`)

	videos := make([]VideoInfo, 0)
	for _, file := range validFiles {
		if !file.IsDir() {
			fileName := file.Name()
			// Replace Clean_and_Jerk with CJ
			fileName2 := strings.ReplaceAll(fileName, "Clean_and_Jerk", "CJ")
			matches := re.FindStringSubmatch(fileName2)
			if len(matches) == 7 {
				date := matches[1]
				hourMinuteSeconds := strings.NewReplacer("h", ":", "m", ":", "s", "").Replace(matches[2])
				name := strings.ReplaceAll(matches[3], "_", " ")
				lift := matches[4]
				attempt := matches[5]
				camera := matches[6]
				displayName := fmt.Sprintf("%s %s - %s - %s - attempt %s - Camera %s",
					date, hourMinuteSeconds, name, lift, attempt, camera)
				// Use forward slashes for URL path
				urlPath := strings.Join([]string{selectedSession, fileName}, "/")
				videos = append(videos, VideoInfo{
					Filename:    urlPath,
					DisplayName: displayName,
				})
			}
		}
	}

	// Sort videos based on parameter
	if sortByAthlete {
		// Sort by athlete name, then by date and time
		sort.Slice(videos, func(i, j int) bool {
			// Extract athlete names from the DisplayName field
			// Format: "date time - name - lift - attempt # - Camera #"
			partsI := strings.Split(videos[i].DisplayName, " - ")
			partsJ := strings.Split(videos[j].DisplayName, " - ")

			if len(partsI) > 1 && len(partsJ) > 1 {
				athleteNameI := partsI[1]
				athleteNameJ := partsJ[1]

				// If athlete names are the same, sort by date and time (which is the first part)
				if athleteNameI == athleteNameJ {
					// The date and time are the first part, so we can compare the original strings
					if ascendingTime {
						// Ascending order (older first)
						return videos[i].Filename < videos[j].Filename
					} else {
						// Descending order (most recent first)
						return videos[i].Filename > videos[j].Filename
					}
				}

				// Otherwise sort by athlete name alphabetically
				return strings.ToLower(athleteNameI) < strings.ToLower(athleteNameJ)
			}

			// Fallback to filename comparison if parsing fails
			return videos[i].Filename > videos[j].Filename
		})
	} else {
		// Already sorted in reverse order (most recent first) by the code above
		// This is the default sort
	}

	// Apply pagination if not showing all videos
	displayVideos := videos
	if !showAll && len(videos) > 20 {
		displayVideos = videos[:20]
	}

	data := TemplateData{
		Videos:               displayVideos,
		StatusMsg:            statusMsg,
		StatusCode:           statusCode,
		Sessions:             sessions,
		SelectedSession:      selectedSession,
		ActiveSession:        state.CurrentSession, // Current competition session
		Platform:             replays.GetCurrentConfig().Platform,
		HasMultiplePlatforms: len(state.AvailablePlatforms) > 1,
		SortByAthlete:        sortByAthlete,
		ShowAll:              showAll,
		TotalCount:           len(videos),
	}

	// Remove the SendStatus call here as it's not needed
	// SendStatus(Ready, fmt.Sprintf("Total videos available: %d", fileCount))

	if err := templates.ExecuteTemplate(w, "videolist.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleWebSocket upgrades HTTP connection to WebSocket
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.ErrorLogger.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	mu.Lock()
	clients[conn] = true
	// Send current status immediately after connection
	if statusMsg != "" {
		// prevent infinite loop if we are reloading after saving videos
		if VideoReadyReloading {
			statusMsg = "Videos ready"
			statusCode = Ready
			if err := conn.WriteJSON(buildStatusMessage(statusCode, statusMsg)); err != nil {
				logging.ErrorLogger.Printf("Failed to send initial status: %v", err)
			}
			VideoReadyReloading = false
		} else {
			if strings.Contains(statusMsg, "Recording") {
				statusCode = Recording
			}
			if err := conn.WriteJSON(buildStatusMessage(statusCode, statusMsg)); err != nil {
				logging.ErrorLogger.Printf("Failed to send initial status: %v", err)
			}
		}
	}
	VideoReadyReloading = false
	mu.Unlock()

	// Keep the connection alive until it closes
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	mu.Lock()
	delete(clients, conn)
	mu.Unlock()
}

// handleMessages broadcasts status messages to all connected WebSocket clients
func handleMessages() {
	for {
		msg := <-broadcast
		mu.Lock()
		statusMsg = msg.Text  // Update the current status message
		statusCode = msg.Code // Update the current status code
		logging.InfoLogger.Printf("Broadcasting status: %s (code: %d)", msg.Text, msg.Code)

		// Broadcast to all connected clients
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				logging.ErrorLogger.Printf("WebSocket error: %v", err)
				client.Close()
				delete(clients, client)
				continue
			}
		}
		mu.Unlock()
	}
}

// StopServer gracefully shuts down the HTTP server
func StopServer() {
	if Server != nil {
		logging.InfoLogger.Println("Shutting down HTTP server...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := Server.Shutdown(ctx); err != nil {
			logging.ErrorLogger.Printf("HTTP server forced shutdown: %v", err)
		} else {
			logging.InfoLogger.Println("HTTP server stopped gracefully")
		}
		Server = nil
	}
}

func currentReplaySessionName() string {
	return strings.ReplaceAll(strings.TrimSpace(state.CurrentSession), " ", "_")
}

func setReplayAPIHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func parseReplayFilename(session string, filename string) (*ParsedReplayFile, bool) {
	matches := replayFilenamePattern.FindStringSubmatch(filename)
	if len(matches) != 7 {
		return nil, false
	}

	attemptNumber, err := strconv.Atoi(matches[5])
	if err != nil {
		attemptNumber = 0
	}

	camera, err := strconv.Atoi(matches[6])
	if err != nil || camera < 1 {
		return nil, false
	}

	return &ParsedReplayFile{
		Session:       session,
		Filename:      filename,
		Timestamp:     matches[1] + "_" + matches[2],
		Athlete:       strings.ReplaceAll(matches[3], "_", " "),
		LiftType:      matches[4],
		AttemptNumber: attemptNumber,
		Camera:        camera,
		URL:           "/videos/" + session + "/" + filename,
	}, true
}

func sanitizeReplaySessionID(session string) (string, error) {
	trimmed := strings.TrimSpace(session)
	if trimmed == "" || trimmed == "." || trimmed == ".." || strings.ContainsAny(trimmed, `/\\`) {
		return "", os.ErrInvalid
	}

	return trimmed, nil
}

func scanReplayFilesForSession(session string) ([]ParsedReplayFile, error) {
	sessionDir := filepath.Join(config.GetVideoDir(), session)
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil, err
	}

	parsed := make([]ParsedReplayFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		replayFile, ok := parseReplayFilename(session, entry.Name())
		if !ok {
			continue
		}

		parsed = append(parsed, *replayFile)
	}

	return parsed, nil
}

func buildGroupedReplayLifts(session string) ([]ReplayLift, error) {
	replayFiles, err := scanReplayFilesForSession(session)
	if err != nil {
		return nil, err
	}

	grouped := make(map[string]*ReplayLift)
	order := make([]string, 0, len(replayFiles))

	for _, replayFile := range replayFiles {
		groupKey := strings.Join([]string{
			replayFile.Timestamp,
			replayFile.Athlete,
			replayFile.LiftType,
			strconv.Itoa(replayFile.AttemptNumber),
		}, "|")

		lift := grouped[groupKey]
		if lift == nil {
			lift = &ReplayLift{
				Timestamp: replayFile.Timestamp,
				Athlete:   replayFile.Athlete,
				LiftType:  replayFile.LiftType,
				Attempt:   replayFile.AttemptNumber,
				Replays:   make([]ReplayFileEntry, 0, 4),
			}
			grouped[groupKey] = lift
			order = append(order, groupKey)
		}

		lift.Replays = append(lift.Replays, ReplayFileEntry{
			Camera:   replayFile.Camera,
			Filename: replayFile.Filename,
			URL:      replayFile.URL,
		})
	}

	lifts := make([]ReplayLift, 0, len(order))
	for _, groupKey := range order {
		lift := grouped[groupKey]
		sort.Slice(lift.Replays, func(i, j int) bool {
			return lift.Replays[i].Camera < lift.Replays[j].Camera
		})
		lift.ReplayCount = len(lift.Replays)
		lifts = append(lifts, *lift)
	}

	return lifts, nil
}

func normalizeReplayAthleteSortKey(athlete string) string {
	return strings.ToLower(strings.TrimSpace(athlete))
}

func sortReplayLifts(lifts []ReplayLift, sortMode string) string {
	effectiveSort := strings.ToLower(strings.TrimSpace(sortMode))
	if effectiveSort != "athlete" {
		effectiveSort = "time"
	}

	sort.Slice(lifts, func(i, j int) bool {
		left := lifts[i]
		right := lifts[j]

		if effectiveSort == "athlete" {
			leftAthlete := normalizeReplayAthleteSortKey(left.Athlete)
			rightAthlete := normalizeReplayAthleteSortKey(right.Athlete)
			if leftAthlete != rightAthlete {
				return leftAthlete < rightAthlete
			}
			if left.Timestamp != right.Timestamp {
				return left.Timestamp > right.Timestamp
			}
		} else {
			if left.Timestamp != right.Timestamp {
				return left.Timestamp > right.Timestamp
			}
			leftAthlete := normalizeReplayAthleteSortKey(left.Athlete)
			rightAthlete := normalizeReplayAthleteSortKey(right.Athlete)
			if leftAthlete != rightAthlete {
				return leftAthlete < rightAthlete
			}
		}

		if left.LiftType != right.LiftType {
			return left.LiftType < right.LiftType
		}

		return left.Attempt < right.Attempt
	})

	return effectiveSort
}

func listReplaySessions() ([]ReplaySessionSummary, error) {
	entries, err := os.ReadDir(config.GetVideoDir())
	if err != nil {
		if os.IsNotExist(err) {
			return make([]ReplaySessionSummary, 0), nil
		}
		return nil, err
	}

	activeSession := currentReplaySessionName()
	type replaySessionItem struct {
		summary ReplaySessionSummary
		modTime time.Time
	}

	items := make([]replaySessionItem, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "unsorted" {
			continue
		}

		groupedLifts, err := buildGroupedReplayLifts(entry.Name())
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}

		info, err := entry.Info()
		if err != nil {
			info = nil
		}

		item := replaySessionItem{
			summary: ReplaySessionSummary{
				ID:        entry.Name(),
				Name:      entry.Name(),
				Active:    entry.Name() == activeSession,
				LiftCount: len(groupedLifts),
			},
		}
		if info != nil {
			item.modTime = info.ModTime()
		}

		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].summary.Active != items[j].summary.Active {
			return items[i].summary.Active
		}
		if !items[i].modTime.Equal(items[j].modTime) {
			return items[i].modTime.After(items[j].modTime)
		}
		return items[i].summary.Name < items[j].summary.Name
	})

	sessions := make([]ReplaySessionSummary, 0, len(items))
	for _, item := range items {
		sessions = append(sessions, item.summary)
	}

	return sessions, nil
}

func handleReplaySessions(w http.ResponseWriter, r *http.Request) {
	setReplayAPIHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	sessions, err := listReplaySessions()
	if err != nil {
		http.Error(w, "Failed to enumerate replay sessions", http.StatusInternalServerError)
		return
	}

	response := ReplaySessionsResponse{
		ActiveSession: currentReplaySessionName(),
		Sessions:      sessions,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logging.ErrorLogger.Printf("Failed to encode replay sessions response: %v", err)
	}
}

func handleReplaySessionLifts(w http.ResponseWriter, r *http.Request) {
	setReplayAPIHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	session, err := sanitizeReplaySessionID(mux.Vars(r)["session"])
	if err != nil {
		http.Error(w, "Invalid session identifier", http.StatusBadRequest)
		return
	}

	sessionDir := filepath.Join(config.GetVideoDir(), session)
	info, err := os.Stat(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Replay session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to inspect replay session", http.StatusInternalServerError)
		return
	}
	if !info.IsDir() {
		http.Error(w, "Replay session not found", http.StatusNotFound)
		return
	}

	lifts, err := buildGroupedReplayLifts(session)
	if err != nil {
		http.Error(w, "Failed to list session replays", http.StatusInternalServerError)
		return
	}

	athleteFilterValue := strings.TrimSpace(r.URL.Query().Get("athlete"))
	var athleteFilter *string
	if athleteFilterValue != "" {
		filteredLifts := make([]ReplayLift, 0, len(lifts))
		for _, lift := range lifts {
			if strings.EqualFold(strings.TrimSpace(lift.Athlete), athleteFilterValue) {
				filteredLifts = append(filteredLifts, lift)
			}
		}
		lifts = filteredLifts
		athleteFilter = &athleteFilterValue
	}

	effectiveSort := sortReplayLifts(lifts, r.URL.Query().Get("sort"))
	activeSession := currentReplaySessionName()
	response := ReplaySessionLiftsResponse{
		Session: ReplaySessionInfo{
			ID:     session,
			Name:   session,
			Active: session == activeSession,
		},
		Sort:          effectiveSort,
		AthleteFilter: athleteFilter,
		LiftCount:     len(lifts),
		Lifts:         lifts,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logging.ErrorLogger.Printf("Failed to encode replay session lifts response: %v", err)
	}
}

func resolveReplaySession() (string, error) {
	session := currentReplaySessionName()
	if session != "" {
		return session, nil
	}

	entries, err := os.ReadDir(config.GetVideoDir())
	if err != nil {
		return "", err
	}

	var latestDir os.DirEntry
	var latestModTime time.Time
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "unsorted" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if latestDir == nil || info.ModTime().After(latestModTime) {
			latestDir = entry
			latestModTime = info.ModTime()
		}
	}

	if latestDir == nil {
		return "", os.ErrNotExist
	}

	return latestDir.Name(), nil
}

func findLatestReplayForCamera(session string, camera int) (*ReplayCameraState, error) {
	if session == "" {
		return nil, os.ErrNotExist
	}

	sessionDir := filepath.Join(config.GetVideoDir(), session)
	files, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil, err
	}

	var latestReplay *ReplayCameraState
	var latestTimestamp string

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		parsedReplay, ok := parseReplayFilename(session, file.Name())
		if !ok {
			continue
		}
		if parsedReplay.Camera != camera {
			continue
		}

		timestamp := parsedReplay.Timestamp
		if latestReplay != nil && timestamp <= latestTimestamp {
			continue
		}

		latestTimestamp = timestamp
		latestReplay = &ReplayCameraState{
			Camera:        camera,
			Available:     true,
			Session:       session,
			Filename:      parsedReplay.Filename,
			VideoPath:     parsedReplay.URL,
			AthleteName:   parsedReplay.Athlete,
			LiftType:      parsedReplay.LiftType,
			AttemptNumber: parsedReplay.AttemptNumber,
			Timestamp:     timestamp,
		}
	}

	if latestReplay == nil {
		return nil, os.ErrNotExist
	}

	return latestReplay, nil
}

func handleReplayState(w http.ResponseWriter, _ *http.Request) {
	setReplayAPIHeaders(w)
	session, err := resolveReplaySession()
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, "Failed to resolve replay state", http.StatusInternalServerError)
		return
	}

	response := ReplayStateResponse{
		ActiveSession:      state.CurrentSession,
		ResolvedSession:    session,
		CurrentAthlete:     state.CurrentAthlete,
		CurrentLiftType:    state.CurrentLiftType,
		CurrentAttempt:     state.CurrentAttempt,
		CurrentCamera:      state.CurrentCameraNumber,
		HasMultiplePlatform: len(state.AvailablePlatforms) > 1,
		Cameras:            make([]ReplayCameraState, 0, 4),
	}

	for camera := 1; camera <= 4; camera++ {
		replayState := ReplayCameraState{Camera: camera}
		if session != "" {
			latestReplay, replayErr := findLatestReplayForCamera(session, camera)
			if replayErr == nil {
				replayState = *latestReplay
			}
		}
		response.Cameras = append(response.Cameras, replayState)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logging.ErrorLogger.Printf("Failed to encode replay state response: %v", err)
	}
}

// handleReplay serves the latest replay for the given camera number from the current session.
// Example filename: 2025-03-29_03h34m34s_DARSIGNY_Shad_CLEANJERK_attempt3_Camera1.mp4
func handleReplay(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	cameraNum := vars["camera"]

	// Accept and strip a .mp4 extension if present in the URL
	cameraNum = strings.TrimSuffix(cameraNum, ".mp4")
	camera, err := strconv.Atoi(cameraNum)
	if err != nil || camera < 1 {
		http.Error(w, "Invalid camera number", http.StatusBadRequest)
		return
	}

	session, err := resolveReplaySession()
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "No active session and no session directories found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to resolve replay session", http.StatusInternalServerError)
		return
	}

	latestReplay, err := findLatestReplayForCamera(session, camera)
	if err != nil {
		http.Error(w, "No replay found for camera "+cameraNum, http.StatusNotFound)
		return
	}

	// Serve the file with correct MIME type for .mp4 and no caching headers
	videoPath := filepath.Join(config.GetVideoDir(), latestReplay.Session, latestReplay.Filename)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Surrogate-Control", "no-store")
	http.ServeFile(w, r, videoPath)
}
