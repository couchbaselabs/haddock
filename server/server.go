package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"strings"
	"sync"
	"time"

	"cod/cluster"
	"cod/events"
	"cod/logger"
	"cod/logs"
	"cod/utils"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type Client struct {
	conn            *websocket.Conn
	watchEventslist map[string]bool
	watchLogs       bool
	logSessionId    string
	eventSessionId  string
}

type Server struct {
	clusters           []string
	clusterConditions  map[string][]map[string]interface{}
	upgrader           websocket.Upgrader
	clients            map[*Client]bool
	eventWatchers      map[string]context.CancelFunc
	logWatcher         context.CancelFunc
	broadcast          chan utils.Message
	clientsMutex       sync.Mutex
	logMutex           sync.Mutex
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

// isAPIRequest determines if the request is for a Couchbase API endpoint
func (s *Server) isAPIRequest(r *http.Request) bool {
	// Check for XHR requests
	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		return true
	}

	// Check Accept header - API requests typically accept JSON or other data formats
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") ||
		strings.Contains(accept, "application/xml") ||
		strings.Contains(accept, "*/*") {
		// But exclude requests that explicitly want HTML
		if !strings.Contains(accept, "text/html") {
			return true
		}
	}

	// Check for other API-specific headers
	if r.Header.Get("Content-Type") == "application/json" {
		return true
	}

	// Also check if this is definitely a browser navigation request
	// Browser navigations typically accept HTML
	if strings.Contains(accept, "text/html") &&
		r.Method == "GET" &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return false
	}

	// If we're still not sure, fall back to the path-based check for common API paths
	apiPrefixes := []string{
		"/pools",
		"/settings",
		"/controller",
		"/nodes",
		"/indexes",
		"/query",
	}

	for _, prefix := range apiPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return true
		}
	}

	return false
}

// handleCouchbaseAPIProxy handles API requests that need to be forwarded to the active Couchbase cluster
func (s *Server) handleCouchbaseAPIProxy(w http.ResponseWriter, r *http.Request) {
	// Determine which cluster to use by checking the referer header
	referer := r.Header.Get("Referer")
	if referer == "" {
		http.Error(w, "API requests require a Referer header to determine target cluster", http.StatusBadRequest)
		return
	}

	// Extract cluster name from the referer
	// Expected format: http://host:port/cui/clustername/...
	refererURL, err := url.Parse(referer)
	if err != nil {
		http.Error(w, "Invalid referer URL", http.StatusBadRequest)
		return
	}

	refPath := refererURL.Path
	if !strings.HasPrefix(refPath, "/cui/") {
		http.Error(w, "Referer path must start with /cui/", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(strings.TrimPrefix(refPath, "/cui/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Could not determine cluster name from referer", http.StatusBadRequest)
		return
	}

	clusterName := parts[0]
	logger.Log.Info("API request to", zap.String("path", r.URL.Path), zap.String("cluster", clusterName), zap.String("referer", referer))

	// Get the namespace from environment variable
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		http.Error(w, "WATCH_NAMESPACE environment variable not set", http.StatusInternalServerError)
		return
	}

	// Create the service name for the Couchbase UI (following standard naming)
	svcName := clusterName + "-ui"

	// Create the target URL - using port 8091 which is the Couchbase UI port
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:8091", svcName, namespace),
	}

	logger.Log.Info("Proxying API request to", zap.String("targetURL", targetURL.String()))

	// Create a reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Handle errors
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Log.Error("API Proxy error", zap.Error(err))
		http.Error(w, fmt.Sprintf("API Proxy error: %v", err), http.StatusBadGateway)
	}

	// Update the Host header and target
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		// Set up the request
		originalDirector(req)
		req.Host = targetURL.Host
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		// Keep the original path
		logger.Log.Info("Final API request", zap.String("requestURL", req.URL.String()))
	}

	// Serve the proxy request
	proxy.ServeHTTP(w, r)
}

// this function is a goroutine that handles the websocket connection
func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	logger.Log.Debug("Handling websocket connection")
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Log.Fatal("Failed to upgrade to websocket", zap.Error(err))
	}
	defer ws.Close()

	client := &Client{
		conn:            ws,
		watchEventslist: make(map[string]bool),
		watchLogs:       false,
		logSessionId:    "",
		eventSessionId:  "",
	}

	s.clientsMutex.Lock()
	s.clients[client] = true
	s.clientsMutex.Unlock()

	// Immediately load and broadcast cluster conditions for the new client
	go func() {
		err := cluster.LoadClusterConditions(s.dynamicClient, s.updateConditions)
		if err != nil {
			logger.Log.Error("Error loading cluster conditions", zap.Error(err))
		}
	}()

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
			logger.Log.Error("Error reading message", zap.Error(err))
			break
		}

		var request struct {
			Type      string   `json:"type"`
			Clusters  []string `json:"clusters,omitempty"`
			Enabled   bool     `json:"enabled,omitempty"`
			SessionID string   `json:"sessionId,omitempty"`
			StartTime string   `json:"startTime,omitempty"`
			EndTime   string   `json:"endTime,omitempty"`
			Follow    bool     `json:"follow,omitempty"`
		}
		if err := json.Unmarshal(message, &request); err != nil {
			logger.Log.Error("Error unmarshalling message", zap.Error(err))
			continue
		}

		switch request.Type {
		case "clustersevents":
			// Set the event session ID
			client.eventSessionId = request.SessionID

			// Update the watched clusters
			client.watchEventslist = make(map[string]bool)
			for _, cluster := range request.Clusters {
				client.watchEventslist[cluster] = true
				s.startWatcher(cluster)

				// Send cached events for this cluster
				go s.sendCachedEvents(client, cluster)
			}
			s.cleanupEventWatchers()

		case "logs":
			client.watchLogs = request.Enabled
			client.logSessionId = request.SessionID

			if request.Enabled {
				var startTime *time.Time
				var endTime *time.Time

				// Handle start time
				if request.StartTime != "" {
					t, err := time.Parse(time.RFC3339, request.StartTime)
					if err == nil {
						startTime = &t
					}
				}

				// Handle end time only if not following
				if !request.Follow && request.EndTime != "" {
					t, err := time.Parse(time.RFC3339, request.EndTime)
					if err == nil {
						endTime = &t
					}
				}

				// Start the log watcher with appropriate parameters
				logger.Log.Debug("Starting log watcher",
					zap.Any("startTime", startTime),
					zap.Any("endTime", endTime),
					zap.Bool("follow", request.Follow))
				s.startLogWatcher(startTime, endTime, request.Follow)
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

	// Load initial events into the cache
	initialEvents := events.GetInitialEvents(s.clientset, s.dynamicClient, clusterName)
	s.eventCacheMutex.Lock()
	s.eventCache[clusterName] = initialEvents
	s.eventCacheMutex.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.eventWatchers[clusterName] = cancel

	// Start the event watcher
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
			s.eventCacheMutex.Lock()
			delete(s.eventCache, cluster)
			s.eventCacheMutex.Unlock()
		}
	}
}

func (s *Server) startLogWatcher(startTime, endTime *time.Time, follow bool) {
	s.logMutex.Lock()
	defer s.logMutex.Unlock()

	if s.logWatcher != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.logWatcher = cancel
	go logs.StartLogWatcher(ctx, s.clientset, s.broadcast, startTime, endTime, follow)
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

		// Cache events
		if msg.Type == "event" {
			s.eventCacheMutex.Lock()
			clusterEvents := s.eventCache[msg.ClusterName]
			if len(clusterEvents) >= 1000 { // Limit cache size
				clusterEvents = clusterEvents[1:] // Remove oldest event
			}
			s.eventCache[msg.ClusterName] = append(clusterEvents, msg)
			s.eventCacheMutex.Unlock()
		}

		s.clientsMutex.Lock()
		for client := range s.clients {
			shouldSend := false
			if msg.Type == "log" {
				shouldSend = client.watchLogs
				msg.SessionID = client.logSessionId
			} else if msg.Type == "event" {
				shouldSend = client.watchEventslist[msg.ClusterName]
				msg.SessionID = client.eventSessionId
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

// Function to handle adding a new cluster
func (s *Server) addCluster(obj interface{}) {
	logger.Log.Debug("addCluster called with object")

	if obj == nil {
		logger.Log.Warn("Received nil object in addCluster")
		return
	}

	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		logger.Log.Warn("Object is not an Unstructured type in addCluster")
		return
	}

	clusterName := unstructuredObj.GetName()
	logger.Log.Debug("Processing cluster addition", zap.String("cluster", clusterName))

	// Only add if not being deleted
	if unstructuredObj.GetDeletionTimestamp() != nil {
		logger.Log.Debug("Skipping add for cluster with deletion timestamp", zap.String("cluster", clusterName))
		return
	}

	// Add the cluster if it doesn't exist
	exists := false
	for _, name := range s.clusters {
		if name == clusterName {
			exists = true
			break
		}
	}

	if !exists {
		s.clusters = append(s.clusters, clusterName)
		logger.Log.Info("Added new cluster to list", zap.String("cluster", clusterName))
		s.broadcastClusters()
	} else {
		logger.Log.Debug("Cluster already in list", zap.String("cluster", clusterName))
	}
}

// Function to handle deleting a cluster
func (s *Server) deleteCluster(obj interface{}) {
	logger.Log.Debug("deleteCluster called with object")

	if obj == nil {
		logger.Log.Warn("Received nil object in deleteCluster")
		return
	}

	var clusterName string

	// Handle the DeletedFinalStateUnknown case first
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		logger.Log.Debug("Received tombstone object in deleteCluster")
		if unstructuredObj, ok := tombstone.Obj.(*unstructured.Unstructured); ok {
			clusterName = unstructuredObj.GetName()
			logger.Log.Info("Extracted cluster name from tombstone", zap.String("cluster", clusterName))
		} else {
			logger.Log.Warn("Tombstone contains unknown object type")
			return
		}
	} else if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
		clusterName = unstructuredObj.GetName()
		logger.Log.Info("Processing cluster deletion", zap.String("cluster", clusterName))
	} else {
		logger.Log.Warn("Received unknown object type in deleteCluster")
		return
	}

	if clusterName == "" {
		logger.Log.Warn("Could not determine cluster name for deletion")
		return
	}

	// Remove the cluster from the list if it exists
	for i, name := range s.clusters {
		if name == clusterName {
			s.clusters = append(s.clusters[:i], s.clusters[i+1:]...)
			logger.Log.Info("Removed cluster from list", zap.String("cluster", clusterName))

			// Also remove from conditions map to prevent memory leaks
			if _, exists := s.clusterConditions[clusterName]; exists {
				delete(s.clusterConditions, clusterName)
				logger.Log.Info("Removed cluster conditions for", zap.String("cluster", clusterName))
			}

			s.broadcastClusters()
			break
		}
	}
}

// Helper function to broadcast the current list of clusters to all clients
func (s *Server) broadcastClusters() {
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()

	for client := range s.clients {
		err := client.conn.WriteJSON(map[string]interface{}{
			"type":     "clusters",
			"clusters": s.clusters,
		})
		if err != nil {
			logger.Log.Error("Error sending cluster update", zap.Error(err))
			client.conn.Close()
			delete(s.clients, client)
		}
	}
}

// Function to handle cluster conditions using the object from the informer
func (s *Server) updateConditions(obj interface{}) {
	logger.Log.Debug("updateConditions called with object")

	if obj != nil {
		unstructuredObj, ok := obj.(*unstructured.Unstructured)
		if ok {
			clusterName := unstructuredObj.GetName()
			logger.Log.Debug("Processing conditions for cluster", zap.String("cluster", clusterName))

			// Always ensure the cluster has an entry in the conditions map
			if _, exists := s.clusterConditions[clusterName]; !exists {
				s.clusterConditions[clusterName] = []map[string]interface{}{}
				logger.Log.Debug("Initialized empty conditions for cluster", zap.String("cluster", clusterName))
			}

			// Extract conditions from the object if available
			status, found, err := unstructured.NestedMap(unstructuredObj.Object, "status")
			if err != nil {
				logger.Log.Error("Error getting status", zap.Error(err))
			} else if found {
				conditions, found, err := unstructured.NestedSlice(status, "conditions")
				if err != nil {
					logger.Log.Error("Error getting conditions", zap.Error(err))
				} else if found && len(conditions) > 0 {
					var conditionsList []map[string]interface{}
					for _, condition := range conditions {
						if condMap, ok := condition.(map[string]interface{}); ok {
							conditionsList = append(conditionsList, condMap)
						}
					}

					// Only update if we found actual conditions
					if len(conditionsList) > 0 {
						s.clusterConditions[clusterName] = conditionsList
						logger.Log.Debug("Updated conditions for cluster", zap.String("cluster", clusterName))
					}
				} else {
					logger.Log.Debug("No conditions found in status for cluster", zap.String("cluster", clusterName))
				}
			} else {
				logger.Log.Debug("No status found in object for cluster", zap.String("cluster", clusterName))
			}

			// Always broadcast the conditions to all connected clients
			// This ensures the UI updates even when there are no conditions yet
			s.broadcastConditions()
		}
	}
}

// Helper function to broadcast the current conditions to all clients
func (s *Server) broadcastConditions() {
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()

	for client := range s.clients {
		err := client.conn.WriteJSON(map[string]interface{}{
			"type":       "clusterConditions",
			"conditions": s.clusterConditions,
		})
		if err != nil {
			logger.Log.Error("Error sending condition update", zap.Error(err))
			client.conn.Close()
			delete(s.clients, client)
		}
	}
}

// handleCouchbaseUIProxy handles reverse proxy requests to Couchbase UI
func (s *Server) handleCouchbaseUIProxy(w http.ResponseWriter, r *http.Request) {
	// Extract the cluster name from the URL path
	// URL format: /cui/clustername/...
	path := r.URL.Path
	parts := strings.SplitN(strings.TrimPrefix(path, "/cui/"), "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Cluster name is required", http.StatusBadRequest)
		return
	}

	clusterName := parts[0]
	logger.Log.Info("Proxying to Couchbase UI for cluster", zap.String("cluster", clusterName), zap.String("path", path))

	// Get the namespace from environment variable
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		http.Error(w, "WATCH_NAMESPACE environment variable not set", http.StatusInternalServerError)
		return
	}

	// Create the service name for the Couchbase UI (following standard naming)
	svcName := clusterName + "-ui"

	// Create the target URL - using port 8091 which is the Couchbase UI port
	targetURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:8091", svcName, namespace),
	}

	//log.Printf("Proxying to: %s", targetURL.String())

	// Create a reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Handle errors
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Log.Error("Proxy error", zap.Error(err))
		http.Error(w, fmt.Sprintf("Proxy error: %v", err), http.StatusBadGateway)
	}

	// Update the Host header and target
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		// First, let the default director set up the request
		originalDirector(req)

		// Set the Host header
		req.Host = targetURL.Host

		// Create a new URL with the target scheme and host
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host

		// Strip /cui/clustername from the path
		cuiPrefix := fmt.Sprintf("/cui/%s", clusterName)
		strippedPath := strings.TrimPrefix(req.URL.Path, cuiPrefix)
		req.URL.Path = strippedPath

		//log.Printf("Proxying request: from %s to %s", path, req.URL.String())
	}

	// Modify the response to handle redirects and add base href
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Handle redirects (3xx responses)
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			if location != "" {
				logger.Log.Info("Original redirect location", zap.String("location", location))
				redirectURL, err := url.Parse(location)
				if err != nil {
					logger.Log.Error("Error parsing redirect URL", zap.Error(err))
				} else {
					// Extract the path and rewrite it to our proxy format
					path := redirectURL.Path
					newLocation := fmt.Sprintf("/cui/%s%s", clusterName, path)
					//log.Printf("Rewriting absolute redirect to: %s", newLocation)
					resp.Header.Set("Location", newLocation)
				}

			}
		}

		return nil
	}

	// Serve the proxy request directly without path rewriting
	proxy.ServeHTTP(w, r)
}

// sendCachedEvents sends cached events for a cluster to a specific client
func (s *Server) sendCachedEvents(client *Client, clusterName string) {
	s.eventCacheMutex.RLock()
	events, exists := s.eventCache[clusterName]
	s.eventCacheMutex.RUnlock()

	if !exists || len(events) == 0 {
		return
	}

	for _, event := range events {
		event.SessionID = client.eventSessionId
		err := client.conn.WriteJSON(event)
		if err != nil {
			logger.Log.Error("Error sending cached event", zap.Error(err))
			return
		}
	}
}
