package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/tiborv/kube-parcel/pkg/shared"
)

// NewPipe creates an io.Pipe
func NewPipe() (*io.PipeReader, *io.PipeWriter) {
	return io.Pipe()
}

// StreamLogs connects to the server and prints logs, returns error if tests fail
func StreamLogs(ctx context.Context, serverURL string) error {
	wsURL := strings.Replace(serverURL, "http", "ws", 1) + "/ws/logs"
	log.Printf("📡 Connecting to log stream: %s", wsURL)

	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Printf("❌ Failed to connect to logs: %v", err)
		return err
	}
	defer c.Close()

	testFailed := false
	lastMessage := ""
	messageCount := 0

	for {
		select {
		case <-ctx.Done():
			if testFailed {
				return fmt.Errorf("tests failed")
			}
			return ctx.Err()

		default:
			_, message, err := c.ReadMessage()
			if err != nil {
				// Connection closed - determine the appropriate error
				if testFailed {
					return fmt.Errorf("tests failed")
				}
				// If we received messages and they indicate progress, provide context
				if messageCount > 0 {
					log.Printf("❌ Connection lost after %d messages. Last: %s", messageCount, lastMessage)
					return fmt.Errorf("runner connection lost during execution (last message: %s)", lastMessage)
				}
				log.Printf("❌ Log stream closed unexpectedly: %v", err)
				return fmt.Errorf("runner connection closed before completion: %w", err)
			}

			messageCount++
			msg, err := parseLogMessage(message)
			if err != nil {
				fmt.Printf("kube-parcel-runner: 🚀 %s\n", string(message))
				lastMessage = string(message)
				continue
			}

			lastMessage = msg.Message
			printLogMessage(msg)

			if result := checkCompletion(msg.Message); result != nil {
				return result.err
			}

			if isTestFailure(msg.Message) {
				testFailed = true
				fmt.Printf("kube-parcel-runner: ❌ TEST FAILURE DETECTED: %s\n", msg.Message)
			}
		}
	}
}

// parseLogMessage attempts to parse a JSON log message
func parseLogMessage(data []byte) (shared.LogMessage, error) {
	var msg shared.LogMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return shared.LogMessage{}, err
	}
	return msg, nil
}

// printLogMessage outputs a formatted log message
func printLogMessage(msg shared.LogMessage) {
	source := "SRV"
	if msg.Source != "" {
		source = strings.ToUpper(msg.Source)
	}
	fmt.Printf("kube-parcel-runner: 🚀 [%s] %s\n", source, msg.Message)

	switch {
	case strings.Contains(msg.Message, "Succeeded:"):
		fmt.Printf("kube-parcel-runner: 🎉 %s\n", msg.Message)
	case strings.Contains(msg.Message, "Failed:"):
		fmt.Printf("kube-parcel-runner: ❌ %s\n", msg.Message)
	}
}

// completionResult represents the result of a completion check
type completionResult struct {
	err error
}

// checkCompletion checks if a message indicates test completion
func checkCompletion(message string) *completionResult {
	if !strings.HasPrefix(message, "COMPLETE:") {
		return nil
	}

	switch {
	case strings.Contains(message, "COMPLETE:FAILED"):
		fmt.Printf("kube-parcel-runner: ❌ Tests completed with failures\n")
		return &completionResult{err: fmt.Errorf("tests failed")}
	case strings.Contains(message, "COMPLETE:SUCCESS"):
		fmt.Printf("kube-parcel-runner: ✅ All tests passed!\n")
		return &completionResult{err: nil}
	}

	return nil
}

// isTestFailure checks if a message indicates a test failure
func isTestFailure(message string) bool {
	failurePatterns := []string{
		"Tests failed for",
		"Integration tests failed",
		"helm test failed",
		"Failed:",
	}
	for _, pattern := range failurePatterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}
