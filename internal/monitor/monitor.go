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

	// First subscribe to config topic
	configTopic := "owlcms/fop/config"
	logging.InfoLogger.Printf("Subscribing to topic %s", configTopic)
	if token := mqttClient.Subscribe(configTopic, 0, nil); token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to subscribe to topic %s: %v", configTopic, token.Error())
		mqttClient.Disconnect(250)
		return
	}

	// Request config and wait for response
	if mqttClient.IsConnected() {
		topic := "owlcms/config"
		token := mqttClient.Publish(topic, 0, false, "requesting configuration")
		if token.Wait() && token.Error() != nil {
			logging.ErrorLogger.Printf("Failed to publish config request: %v", token.Error())
			mqttClient.Disconnect(250)
			return
		}
	}

	// Wait up to 2 seconds for platform list
	var platforms []string
	select {
	case platforms = <-PlatformListChan:
		// Validate configured platform
		if !validatePlatform(cfg, platforms) {
			if len(platforms) == 1 {
				// Auto-select single platform
				platform := platforms[0]
				configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
				if err := config.UpdatePlatform(configFilePath, platform); err != nil {
					logging.ErrorLogger.Printf("Error updating platform: %v", err)
					mqttClient.Disconnect(250)
					return
				}
				logging.InfoLogger.Printf("Automatically selected platform: %s", platform)
				cfg.Platform = platform
			} else if len(platforms) > 1 {
				if ShowPlatformDialogFunc != nil {
					logging.InfoLogger.Println("Multiple platforms detected, showing selection dialog")
					ShowPlatformDialogFunc()
				}
				mqttClient.Disconnect(250)
				return // Don't proceed without platform selection
			}
		}
	case <-time.After(2 * time.Second):
		logging.ErrorLogger.Printf("No response from MQTT broker for platform list")
		mqttClient.Disconnect(250)
		return
	}

	// Don't proceed if no platform is selected
	if cfg.Platform == "" {
		logging.ErrorLogger.Printf("No platform selected, cannot subscribe to MQTT topics")
		mqttClient.Disconnect(250)
		return
	}

	// Subscribe to platform-specific topics only after platform is confirmed
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

func PublishConfig(platform string) {
	if mqttClient == nil {
		logging.ErrorLogger.Printf("MQTT client not initialized")
		return
	}

	// Drain any pending messages from channel
	select {
	case <-PlatformListChan:
	default:
	}

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

	// Send platform list to channel
	PlatformListChan <- configMsg.Platforms
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
