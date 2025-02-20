package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ClientInfo struct {
	ID          string    `json:"id"`
	RemoteAddr  string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
}

type APIResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

type WebSocketEndpoint struct {
	Path      string       `json:"path"`
	CreatedAt time.Time    `json:"created_at"`
	Clients   []ClientInfo `json:"clients"`
}

var (
	clients    = make(map[*websocket.Conn]ClientInfo)
	endpoints  = make(map[string]*WebSocketEndpoint)
	clientsMux sync.RWMutex
	upgrader   = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

func generateEndpointPath() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return "/ws/" + hex.EncodeToString(bytes)
}

func main() {
	http.HandleFunc("/api/endpoints", handleEndpoints)
	http.HandleFunc("/ws/", handleDynamicWebSocket)

	fmt.Println("Listening to port 8080")
	http.ListenAndServe(":8080", nil)
}

func handleEndpoints(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		clientsMux.RLock()
		endpointList := make([]WebSocketEndpoint, 0, len(endpoints))
		for _, endpoint := range endpoints {
			endpointList = append(endpointList, *endpoint)
		}
		clientsMux.RUnlock()

		json.NewEncoder(w).Encode(APIResponse{
			Status:  "success",
			Message: "Active WebSocket endpoints",
			Data:    endpointList,
		})

	case http.MethodPost:
		path := generateEndpointPath()
		endpoint := &WebSocketEndpoint{
			Path:      path,
			CreatedAt: time.Now(),
			Clients:   make([]ClientInfo, 0),
		}

		clientsMux.Lock()
		endpoints[path] = endpoint
		clientsMux.Unlock()

		json.NewEncoder(w).Encode(APIResponse{
			Status:  "success",
			Message: "WebSocket endpoint created",
			Data:    endpoint,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error",
			Message: "Method not allowed",
		})
	}
}

func handleDynamicWebSocket(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	clientsMux.RLock()
	endpoint, exists := endpoints[path]
	clientsMux.RUnlock()

	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error",
			Message: "WebSocket endpoint not found",
		})
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()

	clientInfo := ClientInfo{
		ID:          fmt.Sprintf("%p", conn),
		RemoteAddr:  conn.RemoteAddr().String(),
		ConnectedAt: time.Now(),
	}

	clientsMux.Lock()
	clients[conn] = clientInfo
	endpoint.Clients = append(endpoint.Clients, clientInfo)
	clientsMux.Unlock()

	fmt.Printf("Device connected to %s: %s (%s)\n", path, clientInfo.ID, clientInfo.RemoteAddr)

	defer func() {
		clientsMux.Lock()
		delete(clients, conn)
		for i, c := range endpoint.Clients {
			if c.ID == clientInfo.ID {
				endpoint.Clients = append(endpoint.Clients[:i], endpoint.Clients[i+1:]...)
				break
			}
		}
		clientsMux.Unlock()
	}()

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("Read error:", err)
			break
		}

		clientsMux.RLock()
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
		clientsMux.RUnlock()
	}
}
