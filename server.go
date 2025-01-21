package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello World!")
	})

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Println("Upgrade error:", err)
			return
		}
		defer conn.Close()

		clients[conn] = true
		fmt.Println("Device connected:", conn.RemoteAddr())

		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				fmt.Println("Read error:", err)
				break
			}
			if messageType == websocket.TextMessage {
				fmt.Println(conn.RemoteAddr(), "\"", string(message), "\"")
			}

			for client := range clients {
				if client != conn {
					err = client.WriteMessage(messageType, message)
					if err != nil {
						fmt.Println("Write error:", err)
						client.Close()
						delete(clients, client)
					}
				}
			}
		}
	})

	fmt.Println("Listening to port 8080")
	http.ListenAndServe(":8080", nil)
}

var clients = make(map[*websocket.Conn]bool)
