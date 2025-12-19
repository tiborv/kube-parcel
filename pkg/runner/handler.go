package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tiborv/kube-parcel/pkg/config"
	"github.com/tiborv/kube-parcel/pkg/shared"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for simplicity
	},
}

// Server is the main orchestrator server
type Server struct {
	state     *StateMachine
	k3s       *K3sManager
	helm      *HelmManager
	extractor *TarExtractor
	startTime time.Time
	logBuffer *LogBuffer
	wsClients map[*websocket.Conn]bool
	wsMutex   sync.Mutex
	debug     bool
}

// NewServer creates a new orchestrator server
func NewServer() *Server {
	k3s := NewK3sManager()

	if airgapEnv := os.Getenv("KUBE_PARCEL_AIRGAP"); airgapEnv == "false" || airgapEnv == "0" {
		k3s.Airgap = false
		log.Println("🌐 Online mode enabled via KUBE_PARCEL_AIRGAP=false")
	}

	s := &Server{
		state:     NewStateMachine(),
		k3s:       k3s,
		extractor: NewTarExtractor(),
		startTime: time.Now(),
		logBuffer: NewLogBuffer(1000),
		wsClients: make(map[*websocket.Conn]bool),
		debug:     os.Getenv("KUBE_PARCEL_DEBUG") == "true",
	}

	helmWriter := &SourceLogWriter{buffer: s.logBuffer, source: "helm", broadcast: s.broadcastLog}
	s.helm = NewHelmManager(io.MultiWriter(os.Stdout, helmWriter))

	s.extractor.OnImage(func(name string) {
		s.state.IncrementImages()
		s.broadcastLog("runner", "info", fmt.Sprintf("Extracted image: %s", name))
	})

	s.extractor.OnChart(func(name string) {
		s.state.IncrementCharts()
		s.broadcastLog("runner", "info", fmt.Sprintf("Extracted chart: %s", name))
	})

	s.state.OnTransition(func(from, to shared.State) {
		s.broadcastLog("runner", "info", fmt.Sprintf("State transition: %s → %s", from, to))
	})

	return s
}

// HandleUpload handles the parcel upload endpoint
func (s *Server) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.state.Current() != shared.StateIdle {
		http.Error(w, "Server not in IDLE state", http.StatusConflict)
		return
	}

	log.Println("📦 Receiving parcel stream...")
	s.state.Transition(shared.StateTransferring)

	if err := s.extractor.Extract(r.Body); err != nil {
		log.Printf("Extraction failed: %v", err)
		s.broadcastLog("runner", "error", fmt.Sprintf("Extraction failed: %v", err))
		s.state.Transition(shared.StateIdle)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Println("✅ Parcel extraction complete")
	s.broadcastLog("runner", "info", "Parcel extraction complete")

	go s.startK3s()

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "accepted",
		"state":  s.state.Current().String(),
	})
}

// startK3s starts K3s and installs Helm charts
func (s *Server) startK3s() {
	ctx := context.Background()

	s.state.Transition(shared.StateStarting)

	var logWriter io.Writer
	if s.debug {
		logWriter = io.MultiWriter(os.Stdout, s.logBuffer)
	} else {
		f, err := os.Create("/tmp/k3s.log")
		if err == nil {
			logWriter = f
		} else {
			logWriter = io.Discard
		}
	}

	if err := s.k3s.Start(ctx, logWriter); err != nil {
		log.Printf("K3s startup failed: %v", err)
		s.broadcastLog("k3s", "error", fmt.Sprintf("Startup failed: %v", err))
		s.broadcastLog("runner", "complete", "COMPLETE:FAILED:K3s startup failed")
		s.state.Transition(shared.StateIdle)
		return
	}

	s.state.Transition(shared.StateReady)
	s.broadcastLog("k3s", "info", "K3s is ready")

	s.broadcastLog("runner", "info", "Importing bundled images...")
	if err := ImportImages(); err != nil {
		log.Printf("Warning: image import failed: %v", err)
		s.broadcastLog("runner", "warning", fmt.Sprintf("Image import warning: %v", err))
	}

	err := s.helm.InstallCharts()

	allPassed := err == nil
	if err != nil {
		log.Printf("Helm installation warnings: %v", err)
		s.broadcastLog("helm", "warning", fmt.Sprintf("Installation warnings: %v", err))
		for _, status := range s.helm.GetChartsStatus() {
			if status.Phase == "Failed" {
				allPassed = false
				break
			}
		}
	}

	if allPassed {
		s.broadcastLog("runner", "complete", "COMPLETE:SUCCESS:All tests passed")
		return
	}
	s.broadcastLog("runner", "complete", "COMPLETE:FAILED:Tests failed")
}

// HandleStatus returns the current server status
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	images, charts := s.state.GetCounts()

	var imageList []string
	if s.k3s.IsReady() {
		cmd := exec.Command("ctr", "-a", config.ContainerdSocket, "-n", config.ContainerdNamespace, "images", "list", "-q")
		if out, err := cmd.Output(); err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				if strings.TrimSpace(line) != "" {
					imageList = append(imageList, strings.TrimSpace(line))
				}
			}
		} else {
			log.Printf("Warning: failed to list containerd images: %v", err)
		}
	}

	clusterStatus := "Initializing"
	if s.k3s.IsReady() {
		clusterStatus = "Ready"
	}

	status := shared.StatusResponse{
		State:            s.state.Current().String(),
		Uptime:           int(time.Since(s.startTime).Seconds()),
		K3sReady:         s.k3s.IsReady(),
		ClusterStatus:    clusterStatus,
		ChartsCount:      charts,
		ImagesCount:      images,
		Images:           imageList,
		Charts:           s.helm.GetChartsStatus(),
		ClusterResources: s.helm.FetchAllClusterResources(),
		StartTime:        s.startTime,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// HandleWebSocket handles WebSocket connections for log streaming
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	s.wsMutex.Lock()
	s.wsClients[conn] = true
	s.wsMutex.Unlock()

	defer func() {
		s.wsMutex.Lock()
		delete(s.wsClients, conn)
		s.wsMutex.Unlock()
		conn.Close()
	}()

	for _, logMsg := range s.logBuffer.GetAll() {
		if err := conn.WriteJSON(logMsg); err != nil {
			return
		}
	}

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

// broadcastLog sends a log message to all WebSocket clients
func (s *Server) broadcastLog(source, level, message string) {
	logMsg := shared.LogMessage{
		Timestamp: time.Now(),
		Level:     level,
		Source:    source,
		Message:   message,
	}

	s.logBuffer.Add(logMsg)

	s.wsMutex.Lock()
	defer s.wsMutex.Unlock()

	for conn := range s.wsClients {
		if err := conn.WriteJSON(logMsg); err != nil {
			conn.Close()
			delete(s.wsClients, conn)
		}
	}
}

// LogBuffer stores recent log messages
type LogBuffer struct {
	mu          sync.RWMutex
	messages    []shared.LogMessage
	maxSize     int
	subscribers []chan shared.LogMessage
}

func NewLogBuffer(maxSize int) *LogBuffer {
	return &LogBuffer{
		messages:    make([]shared.LogMessage, 0, maxSize),
		maxSize:     maxSize,
		subscribers: make([]chan shared.LogMessage, 0),
	}
}

func (lb *LogBuffer) Add(msg shared.LogMessage) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	lb.messages = append(lb.messages, msg)
	if len(lb.messages) > lb.maxSize {
		lb.messages = lb.messages[1:]
	}

	for _, ch := range lb.subscribers {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (lb *LogBuffer) GetAll() []shared.LogMessage {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	result := make([]shared.LogMessage, len(lb.messages))
	copy(result, lb.messages)
	return result
}

func (lb *LogBuffer) Subscribe(ch chan shared.LogMessage) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.subscribers = append(lb.subscribers, ch)
}

func (lb *LogBuffer) Unsubscribe(ch chan shared.LogMessage) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	for i, sub := range lb.subscribers {
		if sub == ch {
			lb.subscribers = append(lb.subscribers[:i], lb.subscribers[i+1:]...)
			close(ch)
			break
		}
	}
}

func (lb *LogBuffer) Write(p []byte) (n int, err error) {
	lines := bytes.Split(p, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		lb.Add(shared.LogMessage{
			Timestamp: time.Now(),
			Level:     "info",
			Source:    "k3s",
			Message:   string(line),
		})
	}
	return len(p), nil
}

// SourceLogWriter writes logs with a specific source
type SourceLogWriter struct {
	buffer    *LogBuffer
	source    string
	broadcast func(source, level, message string)
}

func (w *SourceLogWriter) Write(p []byte) (n int, err error) {
	lines := bytes.Split(p, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		// Use broadcast if available (includes websocket)
		if w.broadcast != nil {
			w.broadcast(w.source, "info", string(line))
		} else {
			w.buffer.Add(shared.LogMessage{
				Timestamp: time.Now(),
				Level:     "info",
				Source:    w.source,
				Message:   string(line),
			})
		}
	}
	return len(p), nil
}
