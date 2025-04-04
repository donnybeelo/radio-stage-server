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
	"github.com/pion/webrtc/v3"
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

type WebRTCEndpoint struct {
	Path      string       `json:"path"`
	Name      string       `json:"name"`
	CreatedAt time.Time    `json:"created_at"`
	Clients   []ClientInfo `json:"clients"`
}

type CreateEndpointRequest struct {
	Name string `json:"name"`
}

// WebRTC signaling message types
type SignalingMessage struct {
	Type string      `json:"type"`
	From string      `json:"from"`
	To   string      `json:"to,omitempty"`
	Data interface{} `json:"data"`
}

// SDP offer/answer data
type SDPData struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// ICE candidate data
type ICECandidateData struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid"`        // Changed to pointer type
	SDPMLineIndex *uint16 `json:"sdpMLineIndex"` // Changed to pointer type
}

// Client connection context
type ClientConn struct {
	Info         ClientInfo
	WSConn       *websocket.Conn
	PeerConn     *webrtc.PeerConnection
	AudioTrack   *webrtc.TrackLocalStaticRTP // For sending audio
	EndpointPath string
}

var (
	clients    = make(map[string]*ClientConn)
	endpoints  = make(map[string]*WebRTCEndpoint)
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
	return "/rtc/" + hex.EncodeToString(bytes)
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
	http.HandleFunc("/rtc/", enableCORS(handleWebRTCSignaling))

	fmt.Println("Listening to port 8080")
	http.ListenAndServe(":8080", nil)
}

func handleEndpoints(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		clientsMux.RLock()
		endpointList := make([]WebRTCEndpoint, 0, len(endpoints))
		for _, endpoint := range endpoints {
			endpointList = append(endpointList, *endpoint)
		}
		clientsMux.RUnlock()

		json.NewEncoder(w).Encode(APIResponse{
			Status:  "success",
			Message: "Active WebRTC endpoints",
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
		endpoint := &WebRTCEndpoint{
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
			Message: "WebRTC endpoint created",
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

func createPeerConnection(clientID string, endpointPath string) (*webrtc.PeerConnection, error) {
	// WebRTC configuration with public STUN servers
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"},
			},
		},
	}

	// Create new RTCPeerConnection
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	// Set up event handlers for ICE connection state changes
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		fmt.Printf("Client %s ICE connection state: %s\n", clientID, state.String())

		if state == webrtc.ICEConnectionStateFailed || state == webrtc.ICEConnectionStateClosed || state == webrtc.ICEConnectionStateDisconnected {
			clientsMux.Lock()
			clientConn, exists := clients[clientID]
			if exists {
				// Clean up client when disconnected
				endpoint, endpointExists := endpoints[clientConn.EndpointPath]
				if endpointExists {
					for i, c := range endpoint.Clients {
						if c.ID == clientID {
							endpoint.Clients = append(endpoint.Clients[:i], endpoint.Clients[i+1:]...)
							break
						}
					}
				}
				delete(clients, clientID)
			}
			clientsMux.Unlock()
		}
	})

	// Handle ICE candidates
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		clientsMux.RLock()
		clientConn, exists := clients[clientID]
		clientsMux.RUnlock()

		if !exists {
			return
		}

		// Send ICE candidate to client
		candidateJSON := candidate.ToJSON()
		message := SignalingMessage{
			Type: "ice-candidate",
			From: "server",
			To:   clientID,
			Data: ICECandidateData{
				Candidate:     candidateJSON.Candidate,
				SDPMid:        candidateJSON.SDPMid,        // Now compatible with *string
				SDPMLineIndex: candidateJSON.SDPMLineIndex, // Now compatible with *uint16
			},
		}

		msgBytes, err := json.Marshal(message)
		if err != nil {
			fmt.Println("Error marshaling ICE candidate:", err)
			return
		}

		if err := clientConn.WSConn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
			fmt.Println("Error sending ICE candidate:", err)
		}
	})

	return pc, nil
}

func handleWebRTCSignaling(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	clientsMux.RLock()
	endpoint, exists := endpoints[path]
	clientsMux.RUnlock()

	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error",
			Message: "WebRTC endpoint not found",
		})
		return
	}

	// Upgrade to WebSocket for signaling
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Upgrade error:", err)
		return
	}

	// Generate unique client ID
	clientID := fmt.Sprintf("client-%x", time.Now().UnixNano())

	clientInfo := ClientInfo{
		ID:          clientID,
		RemoteAddr:  conn.RemoteAddr().String(),
		ConnectedAt: time.Now(),
	}

	// Create peer connection
	peerConnection, err := createPeerConnection(clientID, path)
	if err != nil {
		fmt.Println("Failed to create peer connection:", err)
		conn.Close()
		return
	}

	// Store client connection
	clientConn := &ClientConn{
		Info:         clientInfo,
		WSConn:       conn,
		PeerConn:     peerConnection,
		EndpointPath: path,
	}

	clientsMux.Lock()
	clients[clientID] = clientConn
	endpoint.Clients = append(endpoint.Clients, clientInfo)
	clientsMux.Unlock()

	// Send the client their assigned ID
	conn.WriteJSON(SignalingMessage{
		Type: "client-id",
		From: "server",
		Data: clientID,
	})

	fmt.Printf("Device connected to %s: %s (%s)\n", path, clientInfo.ID, clientInfo.RemoteAddr)

	// Setup handler for audio tracks
	peerConnection.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		fmt.Printf("New audio track from client %s\n", clientID)

		if remoteTrack.Kind() != webrtc.RTPCodecTypeAudio {
			fmt.Println("Ignoring non-audio track")
			return
		}

		// Read audio packets and broadcast to other clients
		for {
			// Read RTP packets
			rtp, _, err := remoteTrack.ReadRTP()
			if err != nil {
				fmt.Println("Error reading RTP packet:", err)
				return
			}

			// Broadcast to other clients in the same endpoint
			clientsMux.RLock()
			for _, otherClientInfo := range endpoint.Clients {
				// Don't send to self
				if otherClientInfo.ID == clientID {
					continue
				}

				otherClient, exists := clients[otherClientInfo.ID]
				if exists && otherClient.AudioTrack != nil {
					if err := otherClient.AudioTrack.WriteRTP(rtp); err != nil {
						fmt.Printf("Error writing RTP to client %s: %v\n", otherClientInfo.ID, err)
					}
				}
			}
			clientsMux.RUnlock()
		}
	})

	defer func() {
		conn.Close()
		if clientConn.PeerConn != nil {
			clientConn.PeerConn.Close()
		}
	}()

	// Handle WebRTC signaling messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("Read error:", err)
			break
		}

		var signalMsg SignalingMessage
		if err := json.Unmarshal(message, &signalMsg); err != nil {
			fmt.Println("Invalid signaling message:", err)
			continue
		}

		switch signalMsg.Type {
		case "offer":
			// Handle SDP offer
			var sdpData SDPData
			dataBytes, _ := json.Marshal(signalMsg.Data)
			if err := json.Unmarshal(dataBytes, &sdpData); err != nil {
				fmt.Println("Invalid SDP data:", err)
				continue
			}

			// Set remote description
			err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  sdpData.SDP,
			})
			if err != nil {
				fmt.Println("Error setting remote description:", err)
				continue
			}

			// Create audio track for sending back to this client
			audioTrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeOpus,
			}, "audio", "server-audio")

			if err != nil {
				fmt.Println("Error creating audio track:", err)
			} else {
				// Add track to peer connection
				rtpSender, err := peerConnection.AddTrack(audioTrack)
				if err != nil {
					fmt.Println("Error adding audio track to peer connection:", err)
				} else {
					// Store track for future use
					clientConn.AudioTrack = audioTrack

					// Read RTCP packets to get client feedback
					go func() {
						for {
							if _, _, err := rtpSender.ReadRTCP(); err != nil {
								return
							}
						}
					}()
				}
			}

			// Create answer
			answer, err := peerConnection.CreateAnswer(nil)
			if err != nil {
				fmt.Println("Error creating answer:", err)
				continue
			}

			// Set local description
			err = peerConnection.SetLocalDescription(answer)
			if err != nil {
				fmt.Println("Error setting local description:", err)
				continue
			}

			// Send answer back
			conn.WriteJSON(SignalingMessage{
				Type: "answer",
				From: "server",
				To:   clientID,
				Data: SDPData{
					Type: "answer",
					SDP:  answer.SDP,
				},
			})

		case "ice-candidate":
			// Handle ICE candidate
			var candidateData ICECandidateData
			dataBytes, _ := json.Marshal(signalMsg.Data)
			if err := json.Unmarshal(dataBytes, &candidateData); err != nil {
				fmt.Println("Invalid ICE candidate data:", err)
				continue
			}

			err = peerConnection.AddICECandidate(webrtc.ICECandidateInit{
				Candidate:     candidateData.Candidate,
				SDPMid:        candidateData.SDPMid,        // Already pointer type
				SDPMLineIndex: candidateData.SDPMLineIndex, // Already pointer type
			})
			if err != nil {
				fmt.Println("Error adding ICE candidate:", err)
			}

		case "disconnect":
			fmt.Printf("Client %s disconnecting\n", clientID)
			return
		}
	}
}
