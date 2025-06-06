package httpServer

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/owlcms/replays/internal/config"
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
		Platform:             config.GetCurrentConfig().Platform,
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
			if err := conn.WriteJSON(StatusMessage{Code: statusCode, Text: statusMsg}); err != nil {
				logging.ErrorLogger.Printf("Failed to send initial status: %v", err)
			}
			VideoReadyReloading = false
		} else {
			if strings.Contains(statusMsg, "Recording") {
				statusCode = Recording
			}
			if err := conn.WriteJSON(StatusMessage{Code: statusCode, Text: statusMsg}); err != nil {
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
	if Server != nil { // Use Server instead of server
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := Server.Shutdown(ctx); err != nil {
			logging.ErrorLogger.Printf("Server forced to shutdown: %v", err)
		}
		logging.InfoLogger.Println("Server stopped")
	}
}

// handleReplay serves the latest replay for the given camera number from the current session.
// Example filename: 2025-03-29_03h34m34s_DARSIGNY_Shad_CLEANJERK_attempt3_Camera1.mp4
func handleReplay(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	cameraNum := vars["camera"]

	// Accept and strip a .mp4 extension if present in the URL
	cameraNum = strings.TrimSuffix(cameraNum, ".mp4")

	// Get current session
	session := strings.ReplaceAll(state.CurrentSession, " ", "_")
	if session == "" {
		// No active session, find most recent directory under videos
		videoDir := config.GetVideoDir()
		entries, err := os.ReadDir(videoDir)
		if err != nil {
			http.Error(w, "No active session and failed to read video directory", http.StatusNotFound)
			return
		}
		var dirs []os.DirEntry
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != "unsorted" {
				dirs = append(dirs, entry)
			}
		}
		if len(dirs) == 0 {
			http.Error(w, "No active session and no session directories found", http.StatusNotFound)
			return
		}
		// Sort directories by name descending (assuming session dirs are named so that latest is last alphabetically)
		sort.Slice(dirs, func(i, j int) bool {
			return dirs[i].Name() > dirs[j].Name()
		})
		session = dirs[0].Name()
	}

	// Build session directory path
	sessionDir := filepath.Join(config.GetVideoDir(), session)
	logging.InfoLogger.Printf("handleReplay: sessionDir = %s", sessionDir) // Use logger instead of fmt.Println
	files, err := os.ReadDir(sessionDir)
	if err != nil {
		http.Error(w, "Session directory not found", http.StatusNotFound)
		return
	}

	// Regex to match files for the current camera, e.g. 2025-03-29_03h34m34s_DARSIGNY_Shad_CLEANJERK_attempt3_Camera1.mp4
	// Match: timestamp at start, anything in the middle, then attemptX_CameraY at the end
	re := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{2}h\d{2}m\d{2}s)_.*_attempt\d+_Camera` + cameraNum + `\.mp4$`)

	var latestFile string
	var latestTimestamp string

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		matches := re.FindStringSubmatch(name)
		if len(matches) == 3 {
			// Combine date and time for comparison
			timestamp := matches[1] + "_" + matches[2]
			if timestamp > latestTimestamp {
				latestTimestamp = timestamp
				latestFile = name
			}
		}
	}

	if latestFile == "" {
		http.Error(w, "No replay found for camera "+cameraNum, http.StatusNotFound)
		return
	}

	// Serve the file with correct MIME type for .mp4 and no caching headers
	videoPath := filepath.Join(sessionDir, latestFile)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Surrogate-Control", "no-store")
	http.ServeFile(w, r, videoPath)
}
