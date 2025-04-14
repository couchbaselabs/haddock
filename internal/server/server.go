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
	conn                 *websocket.Conn
	watchEventslist      map[string]bool
	watchEventslistMutex sync.RWMutex // Mutex for watchEventslist
	logWatcher           context.CancelFunc
	logSessionId         string
	eventSessionId       string
	sendingCached        bool
	eventQueue           []utils.Message
	stateMutex           sync.RWMutex // Mutex for client-specific state (sendingCached, eventQueue, session IDs, logWatcher)
}

type Server struct {
	clusters               []string
	clustersMutex          sync.RWMutex // Mutex for protecting clusters slice
	clusterConditions      map[string][]map[string]interface{}
	clusterConditionsMutex sync.RWMutex // Mutex for protecting clusterConditions map
	upgrader               websocket.Upgrader
	clients                map[*Client]bool
	clientsMapMutex        sync.RWMutex // Mutex for clients map
	eventWatchers          map[string]context.CancelFunc
	broadcast              chan utils.Message
	eventWatchersMutex     sync.Mutex
	eventCache             map[string][]utils.Message // Map of clusterName to cached events
	eventCacheMutex        sync.RWMutex
	clientset              *kubernetes.Clientset
	dynamicClient          dynamic.Interface
	allowedMetrics         map[string]bool
	clientQueue            map[*Client]bool // Map of clients with pending events
	clientQueueMutex       sync.Mutex       // Mutex for clientQueue map
}

func NewServer() *Server {
	return &Server{
		upgrader:          websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024},
		clients:           make(map[*Client]bool),
		eventWatchers:     make(map[string]context.CancelFunc),
		broadcast:         make(chan utils.Message),
		clusterConditions: make(map[string][]map[string]interface{}),
		eventCache:        make(map[string][]utils.Message),
		clientQueue:       make(map[*Client]bool),
		clusters:          make([]string, 0), // Initialize clusters slice
		allowedMetrics: map[string]bool{
			"couchbase_operator_cpu_under_management":               true,
			"couchbase_operator_in_place_upgrade_failures":          true,
			"couchbase_operator_memory_under_management_bytes":      true,
			"couchbase_operator_reconcile_failures":                 true,
			"couchbase_operator_pod_replacements_failed":            true,
			"couchbase_operator_pod_recovery_failures_total":        true,
			"couchbase_operator_pod_recoveries_total":               true,
			"couchbase_operator_swap_rebalance_failures":            true,
			"couchbase_operator_swap_rebalances_total":              true,
			"couchbase_operator_volume_size_under_management_bytes": true,
			"couchbase_operator_pod_replacements_total":             true,
			"couchbase_operator_in_place_upgrades_total":            true,
		},
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

	//go routines will be called when a request is made to these endpoints
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
