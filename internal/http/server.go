package http

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
	"github.com/owlcms/replays/internal/websocket"
)

var (
	Server    *http.Server // Make server public
	templates *template.Template
)

type VideoInfo struct {
	Filename    string
	DisplayName string
}

type TemplateData struct {
	Videos     []VideoInfo
	ShowAll    bool
	TotalCount int
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
	x := recording.GetVideoDir()
	logging.InfoLogger.Printf("Serving video files from %s\n", x)
	router.PathPrefix("/videos/").Handler(http.StripPrefix("/videos/", http.FileServer(http.Dir(x))))

	router.HandleFunc("/", listFilesHandler)

	router.HandleFunc("/ws", handleWebSocket)

	addr := fmt.Sprintf(":%d", port)
	Server = &http.Server{
		Addr:    addr,
		Handler: router,
	}

	logging.InfoLogger.Printf("Starting HTTP server on %s\n", addr)
	if err := Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logging.ErrorLogger.Printf("Failed to start server: %v", err)
	}
}

// listFilesHandler lists all files in the videos directory as clickable hyperlinks
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	files, err := os.ReadDir(recording.GetVideoDir())
	if err != nil {
		http.Error(w, "Failed to read videos directory", http.StatusInternalServerError)
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

	showAll := r.URL.Query().Get("showAll") == "true"
	fileCount := len(validFiles)
	if !showAll && fileCount > 20 {
		validFiles = validFiles[:20]
	}

	// Regex to extract date, hour, name, lift type, and attempt
	re := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{2}h\d{2}m\d{2}s)_(.+)_(CLEANJERK|SNATCH)_attempt(\d+)(?:_\d+)?\.mp4$`)

	videos := make([]VideoInfo, 0)
	for _, file := range validFiles {
		if !file.IsDir() {
			fileName := file.Name()
			// Replace Clean_and_Jerk with CJ
			fileName2 := strings.ReplaceAll(fileName, "Clean_and_Jerk", "CJ")
			matches := re.FindStringSubmatch(fileName2)
			if len(matches) == 6 {
				date := matches[1]
				hourMinuteSeconds := strings.NewReplacer("h", ":", "m", ":", "s", "").Replace(matches[2])
				name := strings.ReplaceAll(matches[3], "_", " ")
				lift := matches[4]
				attempt := matches[5]
				displayName := fmt.Sprintf("%s %s - %s - %s - attempt %s",
					date, hourMinuteSeconds, name, lift, attempt)
				videos = append(videos, VideoInfo{
					Filename:    fileName,
					DisplayName: displayName,
				})
			}
		}
	}

	data := TemplateData{
		Videos:     videos,
		ShowAll:    showAll,
		TotalCount: fileCount,
	}

	if err := templates.ExecuteTemplate(w, "videolist.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

// handleWebSocket upgrades HTTP connection to WebSocket
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.ErrorLogger.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	websocket.Clients[conn] = true
	defer delete(websocket.Clients, conn)

	// Keep the connection alive
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
