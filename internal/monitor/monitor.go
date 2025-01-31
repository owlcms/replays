package monitor

import (
	"fmt"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
	"github.com/owlcms/replays/internal/state"
)

// Monitor listens to the owlcms broker for specific messages
func Monitor(cfg *config.Config) {
	// logging.InfoLogger.Println("starting monitor")
	mqttAddress := fmt.Sprintf("tcp://%s:1883", cfg.OwlCMS)
	opts := mqtt.NewClientOptions().AddBroker(mqttAddress)
	opts.SetClientID("replays-monitor")
	opts.SetDefaultPublishHandler(messageHandler())

	// logging.InfoLogger.Println("creating mqtt client")
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		logging.ErrorLogger.Printf("Failed to connect to MQTT broker: %v", token.Error())
		return
	}

	// logging.InfoLogger.Println("subscribing to topics")
	topics := []string{"owlcms/fop/start", "owlcms/fop/stop", "owlcms/fop/refereesDecision"}
	for _, topic := range topics {
		logging.InfoLogger.Printf("Subscribing to topic %s", topic+"/"+cfg.Platform)
		if token := client.Subscribe(topic+"/"+cfg.Platform, 0, nil); token.Wait() && token.Error() != nil {
			logging.ErrorLogger.Printf("Failed to subscribe to topic %s: %v", topic, token.Error())
		}
	}

	logging.InfoLogger.Printf("MQTT monitoring started on %s", mqttAddress)
}

func messageHandler() mqtt.MessageHandler {
	return func(client mqtt.Client, msg mqtt.Message) {
		payload := string(msg.Payload())
		logging.InfoLogger.Printf("Received message: %s %s", msg.Topic(), msg.Payload())

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
		}
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
