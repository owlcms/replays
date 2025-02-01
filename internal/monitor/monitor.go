package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/owlcms/replays/internal/config"
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
)

// Monitor listens to the owlcms broker for specific messages
func Monitor(cfg *config.Config) {
	mqttAddress := fmt.Sprintf("tcp://%s:1883", cfg.OwlCMS)
	opts := mqtt.NewClientOptions().AddBroker(mqttAddress)
	opts.SetClientID("replays-monitor")
	opts.SetDefaultPublishHandler(messageHandler())

	mqttClient = mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to connect to MQTT broker: %v", token.Error())
		return
	}

	// Define topics and subscribe
	platformTopics := []string{
		"owlcms/fop/start",
		"owlcms/fop/stop",
		"owlcms/fop/refereesDecision",
	}

	// Subscribe to platform-specific topics
	for _, topic := range platformTopics {
		fullTopic := topic + "/" + cfg.Platform
		logging.InfoLogger.Printf("Subscribing to topic %s", fullTopic)
		if token := mqttClient.Subscribe(fullTopic, 0, nil); token.Wait() && token.Error() != nil {
			logging.ErrorLogger.Printf("Failed to subscribe to topic %s: %v", fullTopic, token.Error())
		}
	}

	// Subscribe to global config topic
	configTopic := "owlcms/fop/config"
	logging.InfoLogger.Printf("Subscribing to topic %s", configTopic)
	if token := mqttClient.Subscribe(configTopic, 0, nil); token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to subscribe to topic %s: %v", configTopic, token.Error())
	}

	logging.InfoLogger.Printf("MQTT monitoring started on %s", mqttAddress)

	// Publish config request after successful connection
	PublishConfig(cfg.Platform)
}

func PublishConfig(platform string) {
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
		payload := string(msg.Payload())
		// logging.InfoLogger.Printf("Received message: %s %s", msg.Topic(), msg.Payload())

		topicParts := strings.Split(msg.Topic(), "/")
		if len(topicParts) < 3 {
			return
		}
		topic := strings.Join(topicParts[:3], "/")

		switch topic {
		case "owlcms/fop/start":
			handleStart(payload)
		case "owlcms/fop/stop":
			handleStop(payload)
		case "owlcms/fop/refereesDecision":
			handleRefereesDecision()
		case "owlcms/fop/config":
			handleConfig(payload)
		}
	}
}

func handleConfig(payload string) {
	// logging.InfoLogger.Printf("Received config reply: %s", payload)
	var config ConfigMessage
	if err := json.Unmarshal([]byte(payload), &config); err != nil {
		logging.ErrorLogger.Printf("Error parsing config message: %v", err)
		return
	}
	// logging.InfoLogger.Printf("Parsed config: jury=%d, platforms=%v, version=%s",
	// 	config.JurySize, config.Platforms, config.Version)

	// Send platform list to channel
	PlatformListChan <- config.Platforms
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
