package main

import (
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebSocket(t *testing.T) {
	// Start a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "Could not open websocket connection", http.StatusBadRequest)
			return
		}
		defer conn.Close()

		for {
			messageType, p, err := conn.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				break
			}

			// Process the audio data in 'p'
			processedData := processAudioData(p)

			// Forward the processed audio data
			if err := conn.WriteMessage(messageType, processedData); err != nil {
				log.Println("write:", err)
				break
			}
		}
	}))
	defer server.Close()

	// Connect to the server using a WebSocket client
	u := "ws" + server.URL[len("http"):]
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket server: %v", err)
	}

	// Send a test message
	testMessage := []byte("test audio data")
	if err := ws.WriteMessage(websocket.TextMessage, testMessage); err != nil {
		t.Fatalf("Failed to send test message: %v", err)
	}
}
func TestProcessAudioData(t *testing.T) {
	// Test with sample audio data
	testData := []byte("sample audio data")
	expectedData := testData // Assuming processAudioData returns the same data

	processedData := processAudioData(testData)
	if string(processedData) != string(expectedData) {
		t.Errorf("Expected %s but got %s", expectedData, processedData)
	}
}

func TestWebSocketWithRealAudioData(t *testing.T) {
	// Start a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "Could not open websocket connection", http.StatusBadRequest)
			return
		}
		defer conn.Close()

		for {
			messageType, p, err := conn.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				break
			}

			// Process the audio data in 'p'
			processedData := processAudioData(p)

			// Forward the processed audio data
			if err := conn.WriteMessage(messageType, processedData); err != nil {
				log.Println("write:", err)
				break
			}
		}
	}))
	defer server.Close()

	// Connect to the server using a WebSocket client
	u := "ws" + server.URL[len("http"):]
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket server: %v", err)
	}
	defer ws.Close()

	// Send a real audio data (for example purposes, using a simple byte slice)
	realAudioData := []byte("real audio data")
	if err := ws.WriteMessage(websocket.TextMessage, realAudioData); err != nil {
		t.Fatalf("Failed to send real audio data: %v", err)
	}

	// Read the response
	_, response, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	// Verify the response
	if string(response) != string(realAudioData) {
		t.Fatalf("Expected %s but got %s", realAudioData, response)
	}
}