package server

import (
	"context"
	"net/http"
	"os"
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
	// Get the namespace from environment variable - critical for operation
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		logger.Log.Fatal("Cannot start server - WATCH_NAMESPACE environment variable not set")
		return
	}

	logger.Log.Info("Starting server",
		zap.String("namespace", namespace),
		zap.String("port", ":3000"))

	// Set up Kubernetes clients
	config, err := rest.InClusterConfig()
	if err != nil {
		logger.Log.Fatal("Cannot initialize Kubernetes client - failed to get in-cluster config",
			zap.Error(err))
		return
	}

	s.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		logger.Log.Fatal("Cannot initialize Kubernetes client",
			zap.Error(err))
		return
	}

	s.dynamicClient, err = dynamic.NewForConfig(config)
	if err != nil {
		logger.Log.Fatal("Cannot initialize Kubernetes dynamic client",
			zap.Error(err))
		return
	}

	// Start the cluster watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cluster.StartClusterWatcher(ctx, s.dynamicClient, s.addCluster, s.deleteCluster, s.updateConditions)

	// Set up HTTP handlers
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", s.handleRootRoute)
	http.HandleFunc("/cluster/", s.handleClusterRoute)
	http.HandleFunc("/ws", s.handleConnections)
	http.HandleFunc("/cui/", s.handleCouchbaseUIProxy)
	http.HandleFunc("/metrics", s.handleMetricsEndpoint)

	// Start message handler
	go s.handleMessages()

	// Start HTTP server
	logger.Log.Info("Server listening",
		zap.String("port", ":3000"),
		zap.String("namespace", namespace))

	if err := http.ListenAndServe(":3000", nil); err != nil {
		logger.Log.Fatal("Server failed",
			zap.Error(err),
			zap.String("port", ":3000"))
	}
}
