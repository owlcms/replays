package http

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
	"github.com/owlcms/replays/internal/state"
	"github.com/owlcms/replays/internal/websocket"
)

var (
	Server    *http.Server // Make server public
	srv       *http.Server
	verbose   bool
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
func StartServer(port int, verboseLogging bool) {
	verbose = verboseLogging
	router := mux.NewRouter()

	// Serve static files from embedded filesystem
	fileServer := http.FileServer(getFileSystem())
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fileServer))

	// Serve video files
	router.PathPrefix("/videos/").Handler(http.StripPrefix("/videos/", http.FileServer(http.Dir("videos"))))

	router.HandleFunc("/", listFilesHandler)
	router.HandleFunc("/timer", func(w http.ResponseWriter, r *http.Request) {
		timerHandler(w, r, verbose)
	})
	router.HandleFunc("/decision", func(w http.ResponseWriter, r *http.Request) { // Fix the undefined Request error
		decisionHandler(w, r, verbose)
	})
	router.HandleFunc("/update", updateHandler)
	router.HandleFunc("/ws", handleWebSocket)

	addr := fmt.Sprintf(":%d", port)
	Server = &http.Server{ // Use Server instead of server
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
	files, err := os.ReadDir("videos")
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
	re := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{2}h\d{2}m\d{2}s)_(.+)_(CJ|Snatch)_attempt(\d+)(?:_\d+)?\.mp4$`)

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

// timerHandler handles the /timer endpoint
func timerHandler(w http.ResponseWriter, r *http.Request, verbose bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	athleteTimerEventType := r.FormValue("athleteTimerEventType")
	if athleteTimerEventType != "StopTime" && athleteTimerEventType != "StartTime" {
		http.Error(w, "Invalid athleteTimerEventType", http.StatusBadRequest)
		return
	}

	fopName := r.FormValue("fopName")
	fopState := r.FormValue("fopState")
	mode := r.FormValue("mode")
	athleteStartTimeMillis := r.FormValue("athleteStartTimeMillis")
	athleteMillisRemaining := r.FormValue("athleteMillisRemaining")
	fullName := r.FormValue("fullName")
	attemptNumberStr := r.FormValue("attemptNumber")
	liftTypeKey := r.FormValue("liftTypeKey")

	attemptNumber, err := strconv.Atoi(attemptNumberStr)
	if err != nil {
		http.Error(w, "Invalid attemptNumber", http.StatusBadRequest)
		return
	}

	if verbose {
		logging.InfoLogger.Printf("Received /timer request:\n"+
			"    fullName=%s\n"+
			"    attemptNumber=%d\n"+
			"    liftTypeKey=%s\n"+
			"    fopName=%s\n"+
			"    fopState=%s\n"+
			"    mode=%s\n"+
			"    athleteTimerEventType=%s\n"+
			"    athleteStartTimeMillis=%s\n"+
			"    athleteMillisRemaining=%s\n",
			fullName, attemptNumber, liftTypeKey, fopName, fopState, mode, athleteTimerEventType, athleteStartTimeMillis, athleteMillisRemaining)
	}

	if athleteTimerEventType == "StopTime" && state.LastTimerStopTime == 0 {
		logging.InfoLogger.Printf("Received StopTime event for %s, attempt %d", fullName, attemptNumber)
		state.LastTimerStopTime = time.Now().UnixNano() / int64(time.Millisecond)
	}

	if athleteTimerEventType == "StartTime" {
		state.LastStartTime = time.Now().UnixNano() / int64(time.Millisecond)
		if err := recording.StartRecording(fullName, liftTypeKey, attemptNumber); err != nil {
			http.Error(w, fmt.Sprintf("Failed to start recording: %v", err), http.StatusInternalServerError)
			return
		}
	}

	fmt.Fprintf(w, "Timer endpoint received")
}

// decisionHandler handles the /decision endpoint
func decisionHandler(w http.ResponseWriter, r *http.Request, verbose bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form data", http.StatusBadRequest)
		return
	}

	decisionEventType := r.FormValue("decisionEventType")
	fopState := r.FormValue("fopState")
	if fopState != "DECISION_VISIBLE" || decisionEventType == "RESET" {
		if verbose {
			logging.InfoLogger.Printf("Ignoring decision: fopState=%s, decisionEventType=%s", fopState, decisionEventType)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Decision endpoint received")
		return
	}

	mode := r.FormValue("mode")
	competitionName := r.FormValue("competitionName")
	fop := r.FormValue("fop")
	breakValue := r.FormValue("break")
	d1 := r.FormValue("d1")
	d2 := r.FormValue("d2")
	d3 := r.FormValue("d3")
	decisionsVisible := r.FormValue("decisionsVisible")
	down := r.FormValue("down")
	recordKind := r.FormValue("recordKind")
	fullName := r.FormValue("fullName")
	attemptNumberStr := r.FormValue("attemptNumber")
	liftTypeKey := r.FormValue("liftTypeKey")

	attemptNumber, err := strconv.Atoi(attemptNumberStr)
	if err != nil {
		http.Error(w, "Invalid attemptNumber", http.StatusBadRequest)
		return
	}

	if verbose {
		logging.InfoLogger.Printf("Received /decision request:\n"+
			"    decisionEventType=%s\n"+
			"    mode=%s\n"+
			"    competitionName=%s\n"+
			"    fop=%s\n"+
			"    fopState=%s\n"+
			"    break=%s\n"+
			"    d1=%s\n"+
			"    d2=%s\n"+
			"    d3=%s\n"+
			"    decisionsVisible=%s\n"+
			"    down=%s\n"+
			"    recordKind=%s\n"+
			"    fullName=%s\n"+
			"    attemptNumber=%d\n"+
			"    liftTypeKey=%s",
			decisionEventType, mode, competitionName, fop, fopState, breakValue, d1, d2, d3, decisionsVisible, down, recordKind, fullName, attemptNumber, liftTypeKey)
	}

	// Stop recording 2 seconds after receiving a decision
	state.LastDecisionTime = time.Now().UnixNano() / int64(time.Millisecond)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered from panic in decision handler: %v", r)
			}
		}()

		time.Sleep(2 * time.Second)
		if err := recording.StopRecording(state.LastDecisionTime); err != nil {
			logging.ErrorLogger.Printf("%v", err)
		}
		// Remove NotifyClients() from here - it will be called by SendStatus when video is ready
	}()
}

// updateHandler handles the /update endpoint and always returns 200 success
func updateHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
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
