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
	Name      string       `json:"name"`
	CreatedAt time.Time    `json:"created_at"`
	Clients   []ClientInfo `json:"clients"`
}

type CreateEndpointRequest struct {
	Name string `json:"name"`
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

func enableCORS(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		handler(w, r)
	}
}

func main() {
	http.HandleFunc("/api/", enableCORS(handleEndpoints))
	http.HandleFunc("/ws/", enableCORS(handleDynamicWebSocket))

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
		var req CreateEndpointRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(APIResponse{
				Status:  "error",
				Message: "Invalid request body",
			})
			return
		}

		if req.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(APIResponse{
				Status:  "error",
				Message: "Name is required",
			})
			return
		}

		path := generateEndpointPath()
		endpoint := &WebSocketEndpoint{
			Path:      path,
			Name:      req.Name,
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
	upgrader.CheckOrigin = func(r *http.Request) bool {
		return true
	}

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
		// Only broadcast to clients connected to the same endpoint
		for _, client := range endpoint.Clients {
			for c := range clients {
				if c != conn && clients[c].ID == client.ID {
					err = c.WriteMessage(messageType, message)
					if err != nil {
						fmt.Println("Write error:", err)
						c.Close()
						delete(clients, c)
					}
					break
				}
			}
		}
		clientsMux.RUnlock()
	}
}
