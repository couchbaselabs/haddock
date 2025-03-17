package server

import (
	"context"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"

	"cod/internal/cluster"
	"cod/internal/logger"
	"cod/internal/utils"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Client struct {
	conn            *websocket.Conn
	watchEventslist map[string]bool
	logWatcher      context.CancelFunc
	logSessionId    string
	eventSessionId  string
}

type Server struct {
	clusters           []string
	clusterConditions  map[string][]map[string]interface{}
	upgrader           websocket.Upgrader
	clients            map[*Client]bool
	eventWatchers      map[string]context.CancelFunc
	broadcast          chan utils.Message
	clientsMutex       sync.RWMutex
	eventWatchersMutex sync.Mutex
	eventCache         map[string][]utils.Message // Map of clusterName to cached events
	eventCacheMutex    sync.RWMutex
	clientset          *kubernetes.Clientset
	dynamicClient      dynamic.Interface
}

func NewServer() *Server {
	return &Server{
		upgrader:          websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024},
		clients:           make(map[*Client]bool),
		eventWatchers:     make(map[string]context.CancelFunc),
		broadcast:         make(chan utils.Message),
		clusterConditions: make(map[string][]map[string]interface{}),
		eventCache:        make(map[string][]utils.Message),
	}
}

func (s *Server) Start() {
	var err error
	config, err := rest.InClusterConfig()
	if err != nil {
		logger.Log.Fatal("Failed to get in-cluster config", zap.Error(err))
	}

	s.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		logger.Log.Fatal("Failed to create Kubernetes client", zap.Error(err))
	}

	s.dynamicClient, err = dynamic.NewForConfig(config)
	if err != nil {
		logger.Log.Fatal("Failed to create dynamic client", zap.Error(err))
	}

	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		logger.Log.Fatal("WATCH_NAMESPACE environment variable not set")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Start the cluster watcher with separate add and delete functions
	go cluster.StartClusterWatcher(ctx, s.dynamicClient, s.addCluster, s.deleteCluster, s.updateConditions)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// First check if this is an API request
		if s.isAPIRequest(r) {
			s.handleCouchbaseAPIProxy(w, r)
			return
		}

		// Otherwise serve the dashboard
		tmpl, _ := template.ParseFiles("templates/index.html")
		tmpl.Execute(w, s.clusters)
	})

	// Add a handler for cluster-specific pages
	http.HandleFunc("/cluster/", func(w http.ResponseWriter, r *http.Request) {
		// Extract the cluster name from the URL path
		pathParts := strings.Split(r.URL.Path, "/")
		if len(pathParts) < 3 {
			http.NotFound(w, r)
			return
		}

		clusterName := pathParts[2]

		// Check if the cluster exists in our tracked clusters list
		clusterExists := false
		for _, name := range s.clusters {
			if name == clusterName {
				clusterExists = true
				break
			}
		}

		if !clusterExists {
			http.NotFound(w, r)
			return
		}

		// Render the cluster template with the cluster name
		tmpl, err := template.ParseFiles("templates/cluster.html")
		if err != nil {
			logger.Log.Error("Error parsing cluster template", zap.Error(err))
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		data := struct {
			Name string
		}{
			Name: clusterName,
		}

		if err := tmpl.Execute(w, data); err != nil {
			logger.Log.Error("Error executing cluster template", zap.Error(err))
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	})

	http.HandleFunc("/ws", s.handleConnections)

	// Add a handler for the Couchbase UI proxy - maintain trailing slash to ensure path consistency
	http.HandleFunc("/cui/", s.handleCouchbaseUIProxy)

	go s.handleMessages()

	logger.Log.Info("Server started on :3000")
	if err := http.ListenAndServe(":3000", nil); err != nil {
		logger.Log.Fatal("Server failed", zap.Error(err))
	}
}
