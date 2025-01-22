package http

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/owlcms/replays/internal/videos"
)

var server *http.Server

// StartServer starts the HTTP server on the specified port
func StartServer(port int, verbose bool) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", listFilesHandler)
	mux.HandleFunc("/timer", func(w http.ResponseWriter, r *http.Request) {
		timerHandler(w, r, verbose)
	})
	mux.HandleFunc("/decision", func(w http.ResponseWriter, r *http.Request) {
		decisionHandler(w, r, verbose)
	})
	mux.HandleFunc("/update", updateHandler)

	addr := fmt.Sprintf(":%d", port)
	server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	log.Printf("Starting HTTP server on %s\n", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// listFilesHandler lists all files in the videos directory as clickable hyperlinks
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	files, err := os.ReadDir("videos")
	if err != nil {
		http.Error(w, "Failed to read videos directory", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "<html><body><h1>Video Files</h1><ul>")
	for _, file := range files {
		if !file.IsDir() {
			fileName := file.Name()
			fmt.Fprintf(w, `<li><a href="/videos/%s">%s</a></li>`, fileName, fileName)
		}
	}
	fmt.Fprintf(w, "</ul></body></html>")
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

	if athleteTimerEventType == "StartTime" {
		if err := videos.StartRecording(fullName, liftTypeKey, attemptNumber, athleteStartTimeMillis); err != nil {
			http.Error(w, fmt.Sprintf("Failed to start recording: %v", err), http.StatusInternalServerError)
			return
		}
	}

	if verbose {
		log.Printf("Received /timer request:\n"+
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
	mode := r.FormValue("mode")
	competitionName := r.FormValue("competitionName")
	fop := r.FormValue("fop")
	fopState := r.FormValue("fopState")
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
		log.Printf("Received /decision request:\n"+
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
		if err := videos.StopRecording(r.FormValue("athleteStartTimeMillis")); err != nil {
			log.Printf("Failed to stop recording: %v", err)
		}
	}()

	fmt.Fprintf(w, "Decision endpoint received")
}

// updateHandler handles the /update endpoint and always returns 200 success
func updateHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Update endpoint")
}

// StopServer gracefully shuts down the HTTP server
func StopServer() {
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Fatalf("Server forced to shutdown: %v", err)
		}
		log.Println("Server gracefully stopped")
	}
}
