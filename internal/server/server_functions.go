package server

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	"cod/internal/cluster"
	"cod/internal/events"
	"cod/internal/logger"
	"cod/internal/logs"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prom2json"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

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
		logWatcher:      nil,
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
		if client.logWatcher != nil {
			client.logWatcher()
		}

		s.clientsMutex.Lock()
		delete(s.clients, client)
		s.clientsMutex.Unlock()
		s.cleanupEventWatchers()

	}()

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			logger.Log.Error("Error reading message", zap.Error(err))
			break
		}

		var request struct {
			Type        string   `json:"type"`
			Clusters    []string `json:"clusters,omitempty"`
			SessionID   string   `json:"sessionId,omitempty"`
			StartTime   string   `json:"startTime,omitempty"`
			EndTime     string   `json:"endTime,omitempty"`
			Follow      bool     `json:"follow,omitempty"`
			ClusterName string   `json:"clusterName,omitempty"`
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

			if request.SessionID != "" {
				if client.logWatcher != nil {
					client.logWatcher()
				}

				client.logSessionId = request.SessionID
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

				logger.Log.Debug("Starting log watcher",
					zap.Any("startTime", startTime),
					zap.Any("endTime", endTime),
					zap.Bool("follow", request.Follow),
					zap.String("clusterName", request.ClusterName))
				s.startLogWatcher(client, startTime, endTime, request.Follow, request.ClusterName, client.logSessionId)

			} else {
				if client.logWatcher != nil {
					client.logWatcher()
				}
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

func (s *Server) startLogWatcher(client *Client, startTime, endTime *time.Time, follow bool, clusterName string, logSessionId string) {

	ctx, cancel := context.WithCancel(context.Background())
	client.logWatcher = cancel
	go logs.StartLogWatcher(ctx, s.clientset, s.broadcast, startTime, endTime, follow, clusterName, logSessionId)
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

		s.clientsMutex.RLock()
		for client := range s.clients {
			shouldSend := false
			if msg.Type == "log" {
				shouldSend = client.logSessionId == msg.SessionID
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
		s.clientsMutex.RUnlock()
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

// handleRootRoute handles the root route, serving either API requests or the dashboard
func (s *Server) handleRootRoute(w http.ResponseWriter, r *http.Request) {
	// First check if this is an API request
	if s.isAPIRequest(r) {
		s.handleCouchbaseAPIProxy(w, r)
		return
	}

	// Otherwise serve the dashboard
	tmpl, _ := template.ParseFiles("templates/index.html")
	tmpl.Execute(w, s.clusters)
}

// handleClusterRoute handles the cluster-specific pages
func (s *Server) handleClusterRoute(w http.ResponseWriter, r *http.Request) {
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
}

// handleMetricsEndpoint proxies requests to the Prometheus metrics endpoint
func (s *Server) handleMetricsEndpoint(w http.ResponseWriter, r *http.Request) {
	// Make a request to the local Prometheus metrics endpoint
	resp, err := http.Get("http://localhost:8383/metrics")
	if err != nil {
		logger.Log.Error("Failed to get metrics from local endpoint", zap.Error(err))
		http.Error(w, "Failed to fetch metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Check if the response status code is successful
	if resp.StatusCode != http.StatusOK {
		logger.Log.Error("Received non-OK status code from metrics endpoint",
			zap.Int("statusCode", resp.StatusCode))
		http.Error(w, "Failed to fetch metrics: received status "+resp.Status, resp.StatusCode)
		return
	}

	// If the client wants JSON (which is the default for our frontend), use prom2json
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")

		// Create a prom2json handler to convert the metrics
		mfChan := make(chan *dto.MetricFamily, 1024)

		// Create a reader from the response body
		err := prom2json.ParseReader(resp.Body, mfChan)
		if err != nil {
			logger.Log.Error("Failed to parse metrics", zap.Error(err))
			http.Error(w, "Failed to parse metrics: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Convert the metrics channel to prom2json format
		var result []*prom2json.Family
		for mf := range mfChan {
			result = append(result, prom2json.NewFamily(mf))
		}

		// Marshal to JSON
		jsonData, err := json.Marshal(result)
		if err != nil {
			logger.Log.Error("Failed to marshal metrics to JSON", zap.Error(err))
			http.Error(w, "Failed to convert metrics to JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(jsonData)
		return
	}

	// For text format requests, just return the raw Prometheus format
	w.Header().Set("Content-Type", "text/plain")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Log.Error("Failed to read metrics response", zap.Error(err))
		http.Error(w, "Failed to read metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}
