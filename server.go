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
	ProfileType string    `json:"profile_type"` // Added field
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

// Data structure for offer message, including profile type
type OfferData struct {
	SDP         string `json:"sdp"`
	Type        string `json:"type"`
	ProfileType string `json:"profileType"` // Added field
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
	ProfileType  string // Added field
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

	case http.MethodDelete:
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Invalid request body"})
			return
		}
		clientsMux.Lock()
		if _, ok := endpoints[req.Path]; ok {
			delete(endpoints, req.Path)
			clientsMux.Unlock()
			json.NewEncoder(w).Encode(APIResponse{Status: "success", Message: "Stage deleted"})
		} else {
			clientsMux.Unlock()
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(APIResponse{Status: "error", Message: "Stage not found"})
		}
		return

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(APIResponse{
			Status:  "error",
			Message: "Method not allowed",
		})
	}
}

func createPeerConnection(clientID string) (*webrtc.PeerConnection, error) {
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
		// ProfileType will be set later from the offer message
	}

	// Create peer connection
	peerConnection, err := createPeerConnection(clientID)
	if err != nil {
		fmt.Println("Failed to create peer connection:", err)
		conn.Close()
		return
	}

	// Store client connection (ProfileType is initially empty)
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
		// Get the profile type of the sender
		clientsMux.RLock()
		senderConn, senderExists := clients[clientID]
		clientsMux.RUnlock()

		if !senderExists {
			fmt.Println("Sender connection not found for track")
			return
		}
		// Ensure ProfileType is set before proceeding
		if senderConn.ProfileType == "" {
			fmt.Printf("ProfileType not yet set for client %s, ignoring track for now\n", clientID)
			// Read and discard to prevent blocking? Or just return.
			go func() {
				buffer := make([]byte, 1500)
				for {
					if _, _, readErr := remoteTrack.Read(buffer); readErr != nil {
						return
					}
				}
			}()
			return
		}
		senderProfileType := senderConn.ProfileType
		fmt.Printf("New audio track from client %s (Profile: %s)\n", clientID, senderProfileType)

		// Ignore tracks from audience members entirely
		if senderProfileType == "audience" {
			fmt.Printf("Ignoring audio track from audience member %s\n", clientID)
			// Read and discard packets to prevent buffer buildup or blocking the sender
			go func() {
				buffer := make([]byte, 1500)
				for {
					if _, _, readErr := remoteTrack.Read(buffer); readErr != nil {
						// Log read error if needed, then return
						return
					}
				}
			}()
			return
		}

		if remoteTrack.Kind() != webrtc.RTPCodecTypeAudio {
			fmt.Println("Ignoring non-audio track")
			return
		}

		// Read audio packets and broadcast based on profile rules
		for {
			rtp, _, err := remoteTrack.ReadRTP()
			if err != nil {
				if err.Error() == "io: read/write on closed pipe" || err.Error() == "EOF" {
					fmt.Printf("Track read loop ended for client %s: %v\n", clientID, err)
				} else {
					fmt.Println("Error reading RTP packet:", err)
				}
				return // Exit loop on error
			}

			clientsMux.RLock()
			// Use senderConn.EndpointPath which is known to be correct for this client
			currentEndpoint, endpointStillExists := endpoints[senderConn.EndpointPath]
			if !endpointStillExists {
				clientsMux.RUnlock()
				fmt.Printf("Endpoint %s no longer exists, stopping broadcast for client %s\n", senderConn.EndpointPath, clientID)
				return
			}

			// Create a snapshot of clients to iterate over to avoid holding lock too long
			clientsInEndpoint := make([]ClientInfo, len(currentEndpoint.Clients))
			copy(clientsInEndpoint, currentEndpoint.Clients)
			clientsMux.RUnlock() // Release lock before potentially long-running loop

			for _, otherClientInfo := range clientsInEndpoint {
				// Don't send to self
				if otherClientInfo.ID == clientID {
					continue
				}

				// Re-acquire lock briefly to get the other client's connection details
				clientsMux.RLock()
				otherClient, exists := clients[otherClientInfo.ID]
				// Ensure the recipient has an audio track ready to receive data
				hasAudioTrack := exists && otherClient.AudioTrack != nil
				recipientProfileType := ""
				if exists {
					recipientProfileType = otherClient.ProfileType
				}
				clientsMux.RUnlock()

				if !exists || !hasAudioTrack || recipientProfileType == "" {
					// Skip if recipient doesn't exist, doesn't have an audio track, or profile isn't set yet
					continue
				}

				// Determine if sender can be heard by recipient based on rules:
				canHear := false
				switch recipientProfileType {
				case "audience":
					// Audience hears Actors ONLY
					if senderProfileType == "actor" {
						canHear = true
					}
				case "actor":
					// Actors hear Actors and Directors
					if senderProfileType == "actor" || senderProfileType == "director" {
						canHear = true
					}
				case "director":
					// Directors hear Actors and Directors
					if senderProfileType == "actor" || senderProfileType == "director" {
						canHear = true
					}
				}

				if canHear {
					// Re-acquire lock to write to the track
					clientsMux.RLock()
					otherClientToWrite, writeExists := clients[otherClientInfo.ID]
					if writeExists && otherClientToWrite.AudioTrack != nil {
						err := otherClientToWrite.AudioTrack.WriteRTP(rtp)
						if err != nil && err.Error() != "io: read/write on closed pipe" {
							fmt.Printf("Error writing RTP from %s (%s) to client %s (%s): %v\n", clientID, senderProfileType, otherClientInfo.ID, recipientProfileType, err)
						}
					}
					clientsMux.RUnlock()
				}
			}
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
			var offerData OfferData // Use the new struct
			dataBytes, _ := json.Marshal(signalMsg.Data)
			if err := json.Unmarshal(dataBytes, &offerData); err != nil {
				fmt.Println("Invalid offer data:", err)
				continue
			}

			// Validate profile type
			if offerData.ProfileType != "audience" && offerData.ProfileType != "actor" && offerData.ProfileType != "director" {
				fmt.Printf("Invalid profile type received from %s: %s\n", clientID, offerData.ProfileType)
				// Optionally send an error message back and close connection
				conn.WriteJSON(SignalingMessage{Type: "error", Data: "Invalid profile type"})
				return // Close the connection handler loop
			}

			// Store profile type in ClientConn and ClientInfo
			clientsMux.Lock()
			if cConn, ok := clients[clientID]; ok {
				cConn.ProfileType = offerData.ProfileType
				cConn.Info.ProfileType = offerData.ProfileType // Update ClientInfo as well

				// Update the profile type in the endpoint's client list
				if endpoint, endpointExists := endpoints[cConn.EndpointPath]; endpointExists {
					for i := range endpoint.Clients {
						if endpoint.Clients[i].ID == clientID {
							endpoint.Clients[i].ProfileType = offerData.ProfileType
							break
						}
					}
				} else {
					// This case should ideally not happen if endpoint was checked at the start
					fmt.Printf("Error: Endpoint %s not found while setting profile type for client %s\n", cConn.EndpointPath, clientID)
				}
				// Update the clientConn reference directly since it's a pointer
				clientConn.ProfileType = offerData.ProfileType
				clientConn.Info.ProfileType = offerData.ProfileType

			} else {
				// This case should also ideally not happen
				fmt.Printf("Error: Client %s not found while setting profile type\n", clientID)
			}
			clientsMux.Unlock()

			fmt.Printf("Client %s set profile type to: %s\n", clientID, offerData.ProfileType)

			// Set remote description
			err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  offerData.SDP, // Use SDP from offerData
			})
			if err != nil {
				fmt.Println("Error setting remote description:", err)
				continue
			}

			// Always create outgoing audio track; profile filtering is handled in OnTrack.
			audioTrack, trackErr := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeOpus,
			}, "audio", "server-audio-"+clientID) // Unique track ID per client

			if trackErr != nil {
				fmt.Println("Error creating audio track:", trackErr)
			} else {
				rtpSender, addTrackErr := peerConnection.AddTrack(audioTrack)
				if addTrackErr != nil {
					fmt.Println("Error adding audio track to peer connection:", addTrackErr)
				} else {
					// Store track for future use
					clientsMux.Lock()
					if cConn, ok := clients[clientID]; ok {
						cConn.AudioTrack = audioTrack
					}
					clientsMux.Unlock()

					// Read RTCP packets to get client feedback (important for NACKs etc.)
					go func() {
						rtcpBuf := make([]byte, 1500)
						for {
							if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
								// RTCP read errors are expected when connection closes
								return
							}
						}
					}()
				}
			}

			// Create answer
			answer, answerErr := peerConnection.CreateAnswer(nil)
			if answerErr != nil { // Use the new variable name
				fmt.Println("Error creating answer:", answerErr)
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
				Data: SDPData{ // Use original SDPData struct for answer
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
