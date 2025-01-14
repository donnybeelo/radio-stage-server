package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func startServer() error {
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
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
	})

	return http.ListenAndServe(":8080", nil)
}

func main() {
	startServer()
}

// processAudioData is a placeholder for actual audio processing logic
func processAudioData(data []byte) []byte {
	// Implement your audio processing logic here
	return data
}
