package http

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/videos"
)

var server *http.Server

// StartServer starts the HTTP server on the specified port
func StartServer(port int, verbose bool) {
	r := mux.NewRouter()

	// Serve video files
	r.PathPrefix("/videos/").Handler(http.StripPrefix("/videos/", http.FileServer(http.Dir("videos"))))

	// Serve CSS files
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	r.HandleFunc("/", listFilesHandler)
	r.HandleFunc("/timer", func(w http.ResponseWriter, r *http.Request) {
		timerHandler(w, r, verbose)
	})
	r.HandleFunc("/decision", func(w http.ResponseWriter, r *http.Request) { // Fix the undefined Request error
		decisionHandler(w, r, verbose)
	})
	r.HandleFunc("/update", updateHandler)

	addr := fmt.Sprintf(":%d", port)
	server = &http.Server{
		Addr:    addr,
		Handler: r,
	}

	logging.InfoLogger.Printf("Starting HTTP server on %s\n", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

	showAll := r.URL.Query().Get("showAll") == "true"
	fileCount := len(files)
	if !showAll && fileCount > 20 {
		files = files[:20]
	}

	fmt.Fprintf(w, `
		<html>
		<head>
			<title>Video Files</title>
			<link rel="stylesheet" type="text/css" href="/static/styles.css">
			<script type="text/javascript">
				function reloadPage() {
					location.reload();
				}
				document.addEventListener('visibilitychange', function() {
					if (document.visibilityState === 'visible') {
						reloadPage();
					}
				});
1				setInterval(reloadPage, 10000); // Reload every 10 seconds
			</script>
		</head>
		<body>
			<h1>Replays</h1>
			<ul>
	`)

	// Regex to extract date, hour, name, lift type, and attempt
	re := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{2}h\d{2}m\d{2}s)_(.+)_(CJ|Snatch)_attempt(\d+)(?:_\d+)?\.mp4$`)

	for _, file := range files {
		if !file.IsDir() {
			fileName := file.Name()
			// Replace Clean_and_Jerk with CJ
			fileName = strings.ReplaceAll(fileName, "Clean_and_Jerk", "CJ")
			matches := re.FindStringSubmatch(fileName)
			logging.InfoLogger.Printf("Matches: %v", matches) // Log the matches
			if len(matches) == 6 {
				date := matches[1]
				hourMinuteSeconds := matches[2]
				name := matches[3]
				lift := matches[4]
				attempt := matches[5]
				// Fix the hour, minute, seconds to hh:mm:ss format
				hourMinuteSeconds = strings.ReplaceAll(hourMinuteSeconds, "h", ":")
				hourMinuteSeconds = strings.ReplaceAll(hourMinuteSeconds, "m", ":")
				hourMinuteSeconds = strings.ReplaceAll(hourMinuteSeconds, "s", "")
				// Change _ in the name with space
				name = strings.ReplaceAll(name, "_", " ")
				displayName := fmt.Sprintf("%s %s - %s - %s - attempt %s", date, hourMinuteSeconds, name, lift, attempt)
				fmt.Fprintf(w, `<li><a href="/videos/%s" target="_blank" rel="noopener noreferrer">%s</a></li>`, fileName, displayName)
			}
		}
	}

	if !showAll && fileCount > 20 {
		fmt.Fprintf(w, `<li><a href="/?showAll=true">Show all</a></li>`)
	}

	fmt.Fprintf(w, `
			</ul>
		</body>
		</html>
	`)
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

	if athleteTimerEventType == "StartTime" {
		if err := videos.StartRecording(fullName, liftTypeKey, attemptNumber, athleteStartTimeMillis); err != nil {
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

	// Stop recording 5 seconds after receiving a decision
	go func() {
		time.Sleep(5 * time.Second)
		if err := videos.StopRecording(""); err != nil {
			logging.ErrorLogger.Printf("%v", err)
		}
	}()
}

// updateHandler handles the /update endpoint and always returns 200 success
func updateHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// StopServer gracefully shuts down the HTTP server
func StopServer() {
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logging.ErrorLogger.Printf("Server forced to shutdown: %v", err)
		}
		logging.InfoLogger.Println("Server stopped")
	}
}
