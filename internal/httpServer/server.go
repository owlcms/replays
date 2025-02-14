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
	Videos          []VideoInfo
	StatusMsg       string
	Sessions        []string
	SelectedSession string // Currently selected directory
	ActiveSession   string // Current competition session from state
	NoSessions      bool
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
				videos = append(videos, VideoInfo{
					Filename:    filepath.Join(selectedSession, fileName),
					DisplayName: displayName,
				})
			}
		}
	}

	data := TemplateData{
		Videos:          videos,
		StatusMsg:       statusMsg,
		Sessions:        sessions,
		SelectedSession: selectedSession,
		ActiveSession:   state.CurrentSession, // Current competition session
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
		if err := conn.WriteJSON(StatusMessage{Code: Ready, Text: statusMsg}); err != nil {
			logging.ErrorLogger.Printf("Failed to send initial status: %v", err)
		}
	}
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
		statusMsg = msg.Text                                           // Update the current status message
		logging.InfoLogger.Printf("Broadcasting status: %s", msg.Text) // Debug logging

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
