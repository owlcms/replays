package monitor

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/httpServer"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
	"github.com/owlcms/replays/internal/state"
)

// ConfigMessage represents the configuration data from owlcms
type ConfigMessage struct {
	JurySize  int      `json:"jurySize"`
	Platforms []string `json:"platforms"`
	Version   string   `json:"version"`
}

var (
	mqttClient mqtt.Client
	// Channel to notify when platform list is updated
	PlatformListChan = make(chan []string, 1)
	// Add new function to show platform dialog
	ShowPlatformDialogFunc func()
)

// Monitor listens to the owlcms broker for specific messages
func Monitor(cfg *config.Config) {
	// First establish MQTT connection
	mqttAddress := fmt.Sprintf("tcp://%s:1883", cfg.OwlCMS)
	opts := mqtt.NewClientOptions().AddBroker(mqttAddress)
	opts.SetClientID("replays-monitor")
	opts.SetDefaultPublishHandler(messageHandler())

	mqttClient = mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to connect to MQTT broker: %v", token.Error())
		return
	}

	// Wait for connection to be established
	attempts := 0
	maxAttempts := 5
	for !mqttClient.IsConnected() && attempts < maxAttempts {
		time.Sleep(100 * time.Millisecond)
		attempts++
	}

	if !mqttClient.IsConnected() {
		logging.ErrorLogger.Printf("Failed to establish MQTT connection after %d attempts", maxAttempts)
		return
	}

	// First subscribe to config topic
	configTopic := "owlcms/fop/config"
	logging.InfoLogger.Printf("Subscribing to topic %s", configTopic)
	if token := mqttClient.Subscribe(configTopic, 0, nil); token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to subscribe to topic %s: %v", configTopic, token.Error())
		mqttClient.Disconnect(250)
		return
	}

	// Get platform list and validate current platform
	platforms, isValid := GetValidatedPlatforms(cfg)
	if platforms == nil {
		logging.ErrorLogger.Printf("No response from MQTT broker for platform list")
		mqttClient.Disconnect(250)
		return
	}

	if !isValid {
		if !AutoSelectPlatform(cfg, platforms) && len(platforms) > 1 {
			if ShowPlatformDialogFunc != nil {
				logging.InfoLogger.Println("Multiple platforms detected, showing selection dialog")
				ShowPlatformDialogFunc()
			}
			mqttClient.Disconnect(250)
			return // Don't proceed without platform selection
		}
	}

	// Don't proceed if no platform is selected
	if cfg.Platform == "" {
		logging.ErrorLogger.Printf("No platform selected, cannot subscribe to MQTT topics")
		mqttClient.Disconnect(250)
		return
	}

	// Subscribe to platform-specific topics
	platformTopics := []string{
		"owlcms/fop/start",
		"owlcms/fop/stop",
		"owlcms/fop/refereesDecision",
	}

	for _, topic := range platformTopics {
		fullTopic := topic + "/" + cfg.Platform
		logging.InfoLogger.Printf("Subscribing to topic %s", fullTopic)
		if token := mqttClient.Subscribe(fullTopic, 0, nil); token.Wait() && token.Error() != nil {
			logging.ErrorLogger.Printf("Failed to subscribe to topic %s: %v", fullTopic, token.Error())
		}
	}

	logging.InfoLogger.Printf("MQTT monitoring started on %s", mqttAddress)
}

func validatePlatform(cfg *config.Config, platforms []string) bool {
	if cfg.Platform == "" {
		return false
	}
	for _, p := range platforms {
		if p == cfg.Platform {
			return true
		}
	}
	// Platform not found in list, clear it
	cfg.Platform = ""
	return false
}

// GetValidatedPlatforms returns the validated list of platforms and whether the current platform is valid
func GetValidatedPlatforms(cfg *config.Config) ([]string, bool) {
	if mqttClient == nil || !mqttClient.IsConnected() {
		logging.ErrorLogger.Printf("MQTT client not initialized or not connected")
		return nil, false
	}

	// Clean out any pending messages from previous requests
	select {
	case <-PlatformListChan:
	default:
	}

	// Request fresh platform list using existing MQTT client
	topic := "owlcms/config"
	token := mqttClient.Publish(topic, 0, false, "requesting configuration")
	if token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to publish config request: %v", token.Error())
		return nil, false
	}

	// Wait for response
	select {
	case platforms := <-PlatformListChan:
		logging.InfoLogger.Printf("Retrieved platforms from MQTT config: %v", platforms)
		isValid := validatePlatform(cfg, platforms)
		return platforms, isValid
	case <-time.After(2 * time.Second):
		logging.ErrorLogger.Printf("No response from MQTT broker for platform list")
		return nil, false
	}
}

// PublishConfig simplified as it's now only used with the existing connection
func PublishConfig(platform string) {
	topic := "owlcms/config"
	token := mqttClient.Publish(topic, 0, false, "requesting configuration")
	if token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to publish config request: %v", token.Error())
	}
}

func messageHandler() mqtt.MessageHandler {
	return func(client mqtt.Client, msg mqtt.Message) {
		topic := msg.Topic()
		payload := string(msg.Payload())

		// Split topic for message handling
		topicParts := strings.Split(topic, "/")
		if len(topicParts) < 3 {
			return
		}
		topic = strings.Join(topicParts[:3], "/")

		switch topic {
		case "owlcms/fop/start":
			handleStart(payload)
		case "owlcms/fop/stop":
			handleStop(payload)
		case "owlcms/fop/break":
			handleBreak(payload)
		case "owlcms/fop/refereesDecision":
			handleRefereesDecision()
		case "owlcms/fop/config":
			handleConfig(payload)
		}
	}
}

func handleBreak(payload string) {
	if payload == "GROUP_DONE" {
		logging.InfoLogger.Println("Session ended")
		state.CurrentSession = ""                                    // Clear current session
		httpServer.SendStatus(httpServer.Ready, "No active session") // Update web UI with session state
	}
}

func handleConfig(payload string) {
	var configMsg ConfigMessage
	if err := json.Unmarshal([]byte(payload), &configMsg); err != nil {
		logging.ErrorLogger.Printf("Error parsing config message: %v", err)
		return
	}

	// Store available platforms
	state.AvailablePlatforms = configMsg.Platforms

	// Log the available platforms
	logging.InfoLogger.Printf("Available platforms: %v", state.AvailablePlatforms)

	// Send platform list to channel
	select {
	case PlatformListChan <- configMsg.Platforms:
	default:
		// If the channel is full, do not block
	}
}

func handleStart(payload string) {
	// Handle start message
	logging.InfoLogger.Printf("Handling start message: %s", payload)
	state.UpdateStateFromStartMessage(payload)
	if err := recording.StartRecording(state.CurrentAthlete, state.CurrentLiftType, state.CurrentAttempt); err != nil {
		logging.ErrorLogger.Printf("Failed to start recording: %v", err)
		return
	}
}

func handleStop(payload string) {
	// Handle stop message
	logging.InfoLogger.Printf("Handling stop message: %s", payload)
	state.UpdateStateFromStopMessage(payload)
}

func handleRefereesDecision() {
	// Handle refereesDecision message
	logging.InfoLogger.Printf("Handling refereesDecision message")
	state.LastDecisionTime = time.Now().UnixNano() / int64(time.Millisecond)
	logging.InfoLogger.Println("Trimming video")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logging.ErrorLogger.Printf("Recovered from panic in decision handler: %v", r)
			}
		}()

		time.Sleep(2 * time.Second)
		if err := recording.StopRecording(state.LastDecisionTime); err != nil {
			logging.ErrorLogger.Printf("Error during trimming: %v", err)
			return
		}
	}()
}

// AutoSelectPlatform attempts to automatically select a platform when there's only one available
func AutoSelectPlatform(cfg *config.Config, platforms []string) bool {
	if len(platforms) == 1 {
		// Auto-select single platform
		platform := platforms[0]
		configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
		if err := config.UpdatePlatform(configFilePath, platform); err != nil {
			logging.ErrorLogger.Printf("Error updating platform: %v", err)
			return false
		}
		logging.InfoLogger.Printf("Automatically selected platform: %s", platform)
		cfg.Platform = platform
		return true
	}
	return false
}
