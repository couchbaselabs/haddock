package server

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"sync"

	"cod/debug"
	"cod/events"
	"cod/logs"
	"cod/utils"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Client struct {
	conn            *websocket.Conn
	watchEventslist map[string]bool
	watchLogs       bool
	logSessionId    string
}

type Server struct {
	clusters           []string
	upgrader           websocket.Upgrader
	clients            map[*Client]bool
	eventWatchers      map[string]context.CancelFunc
	logWatcher         context.CancelFunc
	broadcast          chan utils.Message
	clientsMutex       sync.Mutex
	logMutex           sync.Mutex
	eventWatchersMutex sync.Mutex
	clientset          *kubernetes.Clientset
	dynamicClient      dynamic.Interface
}

func NewServer() *Server {
	return &Server{
		upgrader:      websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024},
		clients:       make(map[*Client]bool),
		eventWatchers: make(map[string]context.CancelFunc),
		broadcast:     make(chan utils.Message),
	}
}

func (s *Server) Start() {
	var err error
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to get in-cluster config: %v", err)
	}

	s.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	s.dynamicClient, err = dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create dynamic client: %v", err)
	}

	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		log.Fatalf("WATCH_NAMESPACE environment variable not set")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go utils.StartClusterWatcher(ctx, s.dynamicClient, s.updateClusters)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl, _ := template.ParseFiles("templates/index.html")
		tmpl.Execute(w, s.clusters)
	})

	http.HandleFunc("/ws", s.handleConnections)

	go s.handleMessages()

	log.Println("Server started on :3000")
	log.Fatal(http.ListenAndServe(":3000", nil))
}

// this function is a goroutine that handles the websocket connection
func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	//debug.Println("Handling ws connection")
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatalf("Failed to upgrade to websocket: %v", err)
	}
	defer ws.Close()

	client := &Client{
		conn:            ws,
		watchEventslist: make(map[string]bool),
		watchLogs:       false,
		logSessionId:    "",
	}

	s.clientsMutex.Lock()
	s.clients[client] = true
	s.clientsMutex.Unlock()

	defer func() {
		s.clientsMutex.Lock()
		delete(s.clients, client)
		s.clientsMutex.Unlock()
		s.cleanupEventWatchers()
		s.checkAndStopLogWatcher()
	}()

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Error reading message: %v", err)
			break
		}

		var request struct {
			Type      string   `json:"type"`
			Clusters  []string `json:"clusters,omitempty"`
			Enabled   bool     `json:"enabled,omitempty"`
			SessionID string   `json:"sessionId,omitempty"`
		}
		if err := json.Unmarshal(message, &request); err != nil {
			log.Printf("Error unmarshalling message: %v", err)
			continue
		}

		switch request.Type {
		case "clusters":
			client.watchEventslist = make(map[string]bool)
			for _, cluster := range request.Clusters {
				client.watchEventslist[cluster] = true
				s.startWatcher(cluster)
			}
			s.cleanupEventWatchers()

		case "logs":
			client.watchLogs = request.Enabled
			client.logSessionId = request.SessionID
			debug.Println("watchLogs", client.watchLogs)
			if client.watchLogs {
				s.startLogWatcher()
			} else {
				s.checkAndStopLogWatcher()
			}
		}
	}
}

func (s *Server) startWatcher(clusterName string) {
	s.eventWatchersMutex.Lock()
	defer s.eventWatchersMutex.Unlock()

	if _, exists := s.eventWatchers[clusterName]; exists {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.eventWatchers[clusterName] = cancel
	go events.StartEventWatcher(ctx, s.clientset, s.dynamicClient, clusterName, s.broadcast)
}

func (s *Server) cleanupEventWatchers() {
	s.eventWatchersMutex.Lock()
	defer s.eventWatchersMutex.Unlock()

	activeClusters := make(map[string]bool)
	s.clientsMutex.Lock()
	for client := range s.clients {
		for cluster := range client.watchEventslist {
			activeClusters[cluster] = true
		}
	}
	s.clientsMutex.Unlock()

	for cluster, cancel := range s.eventWatchers {
		if !activeClusters[cluster] {
			cancel()
			delete(s.eventWatchers, cluster)
		}
	}
}

func (s *Server) startLogWatcher() {
	s.logMutex.Lock()
	defer s.logMutex.Unlock()

	if s.logWatcher != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.logWatcher = cancel
	go logs.StartLogWatcher(ctx, s.clientset, s.broadcast)
}

func (s *Server) checkAndStopLogWatcher() {
	s.clientsMutex.Lock()
	// Check if any client still wants logs
	logsNeeded := false
	for client := range s.clients {
		if client.watchLogs {
			logsNeeded = true
			break
		}
	}
	s.clientsMutex.Unlock()

	// If no client needs logs, stop the watcher
	if !logsNeeded {
		s.logMutex.Lock()
		if s.logWatcher != nil {
			s.logWatcher()
			s.logWatcher = nil
		}
		s.logMutex.Unlock()
	}
}

func (s *Server) handleMessages() {
	for {
		msg := <-s.broadcast
		s.clientsMutex.Lock()
		for client := range s.clients {
			shouldSend := false
			if msg.Type == "log" {
				shouldSend = client.watchLogs
				debug.Println("shouldSend", shouldSend)
				msg.SessionID = client.logSessionId
			} else {
				shouldSend = client.watchEventslist[msg.ClusterName]
			}

			if shouldSend {
				err := client.conn.WriteJSON(msg)
				if err != nil {
					client.conn.Close()
					delete(s.clients, client)
				}
			}
		}
		s.clientsMutex.Unlock()
	}
}

func (s *Server) updateClusters() {
	namespace := os.Getenv("WATCH_NAMESPACE")
	newClusters, err := utils.GetCouchbaseClusters(s.dynamicClient, namespace)
	if err != nil {
		log.Printf("Error updating Couchbase clusters: %v", err)
		return
	}
	s.clusters = newClusters

	// Broadcast the new list of clusters to all connected clients
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()
	for client := range s.clients {
		err := client.conn.WriteJSON(map[string]interface{}{
			"type":     "clusters",
			"clusters": newClusters,
		})
		if err != nil {
			log.Printf("Error sending cluster update: %v", err)
			client.conn.Close()
			delete(s.clients, client)
		}
	}
}
