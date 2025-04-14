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
	"cod/internal/utils"

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
	remoteAddr := r.RemoteAddr
	userAgent := r.UserAgent()

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Log.Error("Failed to upgrade to websocket",
			zap.Error(err),
			zap.String("remoteAddr", remoteAddr))
		http.Error(w, "WebSocket upgrade failed", http.StatusInternalServerError)
		return
	}
	defer ws.Close()

	client := &Client{
		conn:            ws,
		watchEventslist: make(map[string]bool),
		logWatcher:      nil,
		logSessionId:    "",
		eventSessionId:  "",
		sendingCached:   false,
		eventQueue:      make([]utils.Message, 0),
	}

	s.clientsMapMutex.Lock()
	s.clients[client] = true
	clientCount := len(s.clients)
	s.clientsMapMutex.Unlock()

	logger.Log.Info("Client connected",
		zap.String("remoteAddr", remoteAddr),
		zap.String("userAgent", userAgent),
		zap.Int("activeClients", clientCount))

	// Immediately load and broadcast cluster conditions for the new client
	go func() {
		err := cluster.LoadClusterConditions(s.dynamicClient, s.updateConditions)
		if err != nil {
			logger.Log.Error("Failed to load cluster conditions for new client",
				zap.Error(err),
				zap.String("remoteAddr", remoteAddr))
		}
	}()

	defer func() {
		client.stateMutex.Lock()
		logWatcher := client.logWatcher
		logSessionId := client.logSessionId
		client.stateMutex.Unlock()

		if logWatcher != nil {
			logWatcher()
			logger.Log.Debug("Client log watcher canceled",
				zap.String("sessionId", logSessionId),
				zap.String("remoteAddr", remoteAddr))
		}

		// Remove from clients map (Lock A, Op A, Unlock A)
		s.clientsMapMutex.Lock()
		delete(s.clients, client)
		remainingClients := len(s.clients) // Capture count while locked
		s.clientsMapMutex.Unlock()

		// Remove from client queue map (Lock B, Op B, Unlock B)
		s.clientQueueMutex.Lock()
		delete(s.clientQueue, client) // Remove from clientQueue as well
		s.clientQueueMutex.Unlock()

		logger.Log.Info("Client disconnected",
			zap.String("remoteAddr", remoteAddr),
			zap.Int("remainingClients", remainingClients))

		s.cleanupEventWatchers()
	}()

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			// Don't log normal websocket close as an error
			if !strings.Contains(err.Error(), "websocket: close") {
				logger.Log.Info("WebSocket connection closed",
					zap.Error(err),
					zap.String("remoteAddr", remoteAddr))
			}
			break
		}

		var request struct {
			Type       string          `json:"type"`
			Clusters   []string        `json:"clusters,omitempty"`
			SessionID  string          `json:"sessionId,omitempty"`
			StartTime  string          `json:"startTime,omitempty"`
			EndTime    string          `json:"endTime,omitempty"`
			Follow     bool            `json:"follow,omitempty"`
			ClusterMap map[string]bool `json:"clusterMap,omitempty"`
		}
		if err := json.Unmarshal(message, &request); err != nil {
			logger.Log.Error("Failed to parse client message",
				zap.Error(err),
				zap.String("rawMessage", string(message)),
				zap.String("remoteAddr", remoteAddr))
			continue
		}

		switch request.Type {
		case "clustersevents":
			// Set the event session ID
			client.stateMutex.Lock()
			client.eventSessionId = request.SessionID
			client.stateMutex.Unlock()

			logger.Log.Debug("Client requested cluster events",
				zap.String("sessionId", request.SessionID),
				zap.Strings("clusters", request.Clusters),
				zap.String("remoteAddr", remoteAddr))

			// Clear any pending event queue for this client
			client.stateMutex.Lock()
			client.eventQueue = nil
			client.stateMutex.Unlock()

			// Remove client from the server's processing queue if present
			s.clientQueueMutex.Lock()
			delete(s.clientQueue, client)
			s.clientQueueMutex.Unlock()

			// Update the watched clusters (with lock)
			client.watchEventslistMutex.Lock()
			client.watchEventslist = make(map[string]bool)
			for _, cluster := range request.Clusters {
				client.watchEventslist[cluster] = true
				s.startWatcher(cluster) // this will start an event watcher if not already running

				// Send cached events for this cluster (asynchronously)
				go s.sendCachedEvents(client, cluster)
			}
			client.watchEventslistMutex.Unlock()
			s.cleanupEventWatchers()

		case "logs":
			if request.SessionID != "" {
				// Cancel existing log watcher if any
				client.stateMutex.Lock()
				logWatcher := client.logWatcher
				prevLogSessionId := client.logSessionId
				client.logSessionId = request.SessionID // Set new session ID while locked
				client.stateMutex.Unlock()

				if logWatcher != nil {
					logWatcher()
					logger.Log.Debug("Previous log watcher canceled",
						zap.String("prevSessionId", prevLogSessionId),
						zap.String("newSessionId", request.SessionID))
				}

				var startTime *time.Time
				var endTime *time.Time

				// Handle start time
				if request.StartTime != "" {
					t, err := time.Parse(time.RFC3339, request.StartTime)
					if err == nil {
						startTime = &t
					} else {
						logger.Log.Warn("Invalid start time format",
							zap.String("startTime", request.StartTime),
							zap.String("sessionId", request.SessionID),
							zap.String("remoteAddr", remoteAddr))
					}
				}

				// Handle end time only if not following
				if !request.Follow && request.EndTime != "" {
					t, err := time.Parse(time.RFC3339, request.EndTime)
					if err == nil {
						endTime = &t
					} else {
						logger.Log.Warn("Invalid end time format",
							zap.String("endTime", request.EndTime),
							zap.String("sessionId", request.SessionID),
							zap.String("remoteAddr", remoteAddr))
					}
				}

				s.startLogWatcher(client, startTime, endTime, request.Follow, request.ClusterMap, request.SessionID)
			} else {
				// Stop log watcher if session ID is empty
				client.stateMutex.Lock()
				logWatcher := client.logWatcher
				logSessionId := client.logSessionId
				client.logSessionId = "" // Clear session ID while locked
				client.logWatcher = nil  // Clear watcher reference while locked
				client.stateMutex.Unlock()

				if logWatcher != nil {
					logWatcher() // Call cancel func after releasing lock
					logger.Log.Debug("Log watcher stopped",
						zap.String("sessionId", logSessionId),
						zap.String("remoteAddr", remoteAddr))
					// client.logSessionId = "" // Moved clearing this under lock above
				}
			}
		}
	}
}

func (s *Server) startWatcher(clusterName string) {
	s.eventWatchersMutex.Lock()
	defer s.eventWatchersMutex.Unlock()

	if _, exists := s.eventWatchers[clusterName]; exists {
		logger.Log.Debug("Event watcher already exists for cluster",
			zap.String("cluster", clusterName))
		return
	}

	// Load initial events into the cache
	initialEvents := events.GetInitialEvents(s.clientset, s.dynamicClient, clusterName)
	eventCount := len(initialEvents)

	logger.Log.Info("Starting event watcher for cluster",
		zap.String("cluster", clusterName),
		zap.Int("initialEventCount", eventCount))

	s.eventCacheMutex.Lock()
	s.eventCache[clusterName] = initialEvents
	s.eventCacheMutex.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.eventWatchers[clusterName] = cancel

	// Start the event watcher (this will send events to the broadcast channel which get added to the cache)
	go events.StartEventWatcher(ctx, s.clientset, s.dynamicClient, clusterName, s.broadcast)
}

func (s *Server) cleanupEventWatchers() {
	s.eventWatchersMutex.Lock()
	defer s.eventWatchersMutex.Unlock()

	// Build a map of currently active clusters from clients
	activeClusters := make(map[string]bool)
	s.clientsMapMutex.RLock()
	for client := range s.clients {
		client.watchEventslistMutex.RLock()
		for cluster := range client.watchEventslist {
			activeClusters[cluster] = true
		}
		client.watchEventslistMutex.RUnlock()
	}
	s.clientsMapMutex.RUnlock()

	initialWatcherCount := len(s.eventWatchers)
	removedCount := 0

	// Remove watchers for clusters with no active clients
	for cluster, cancel := range s.eventWatchers {
		if !activeClusters[cluster] {
			cancel()
			delete(s.eventWatchers, cluster)

			// Also clean up cached events
			s.eventCacheMutex.Lock()
			cachedEventCount := 0
			if events, exists := s.eventCache[cluster]; exists {
				cachedEventCount = len(events)
				delete(s.eventCache, cluster)
			}
			s.eventCacheMutex.Unlock()

			logger.Log.Info("Removed event watcher for inactive cluster",
				zap.String("cluster", cluster),
				zap.Int("cachedEventsRemoved", cachedEventCount))

			removedCount++
		}
	}

	if removedCount > 0 {
		logger.Log.Info("Cleaned up unused event watchers",
			zap.Int("removedWatchers", removedCount),
			zap.Int("initialWatcherCount", initialWatcherCount),
			zap.Int("remainingWatchers", len(s.eventWatchers)))
	} else if initialWatcherCount > 0 {
		logger.Log.Debug("No event watchers to clean up",
			zap.Int("activeWatchers", initialWatcherCount))
	}
}

func (s *Server) startLogWatcher(client *Client, startTime, endTime *time.Time, follow bool, clusterNames map[string]bool, logSessionId string) {
	logContext := []zap.Field{
		zap.String("sessionId", logSessionId),
		zap.Any("clusterNames", clusterNames),
		zap.Bool("follow", follow),
	}

	if startTime != nil {
		logContext = append(logContext, zap.Time("startTime", *startTime))
	}

	if endTime != nil {
		logContext = append(logContext, zap.Time("endTime", *endTime))
	}

	logger.Log.Info("Starting log watcher for client", logContext...)

	ctx, cancel := context.WithCancel(context.Background())
	client.stateMutex.Lock()
	client.logWatcher = cancel
	client.stateMutex.Unlock()
	go logs.StartLogWatcher(ctx, s.clientset, s.broadcast, startTime, endTime, follow, clusterNames, logSessionId)
}

func (s *Server) handleMessages() {
	logger.Log.Info("Starting message handler")

	for {
		// --- Process Queued Client Events First ---
		s.clientQueueMutex.Lock()
		clientsToProcessQueue := make([]*Client, 0, len(s.clientQueue))
		for client := range s.clientQueue {
			clientsToProcessQueue = append(clientsToProcessQueue, client)
		}
		s.clientQueueMutex.Unlock()

		for _, client := range clientsToProcessQueue {
			client.stateMutex.Lock()
			sendingCached := client.sendingCached
			queueLen := len(client.eventQueue)
			client.stateMutex.Unlock()

			if sendingCached {
				continue // Still sending cached, skip for now
			}

			if queueLen > 0 {
				logger.Log.Debug("Processing queued events for client", zap.Int("queuedEvents", queueLen))

				client.stateMutex.Lock()
				eventQueueCopy := make([]utils.Message, queueLen)
				copy(eventQueueCopy, client.eventQueue)
				client.eventQueue = nil
				client.stateMutex.Unlock()

				s.clientsMapMutex.RLock()
				_, clientExists := s.clients[client]
				s.clientsMapMutex.RUnlock()

				if !clientExists {
					s.clientQueueMutex.Lock()
					delete(s.clientQueue, client)
					s.clientQueueMutex.Unlock()
					continue
				}

				processedSuccessfully := true
				for _, queuedMsg := range eventQueueCopy {
					if !s.sendToClient(client, queuedMsg) {
						processedSuccessfully = false
						break // Stop processing for this disconnected client
					}
				}

				if processedSuccessfully {
					s.clientQueueMutex.Lock()
					delete(s.clientQueue, client)
					s.clientQueueMutex.Unlock()
				}
			} else {
				// No events in queue, just remove from clientQueue
				s.clientQueueMutex.Lock()
				delete(s.clientQueue, client)
				s.clientQueueMutex.Unlock()
			}
		}

		// --- Process Next Message from Broadcast Channel ---
		msg := <-s.broadcast

		switch msg.Type {
		case "clustersListUpdate":
			logger.Log.Debug("Broadcasting clustersListUpdate")
			broadcastPayload := map[string]interface{}{"type": "clusters", "clusters": msg.Clusters}
			s.broadcastToAllClients(broadcastPayload)

		case "conditionsUpdate":
			logger.Log.Debug("Broadcasting conditionsUpdate")
			broadcastPayload := map[string]interface{}{"type": "clusterConditions", "conditions": msg.Conditions}
			s.broadcastToAllClients(broadcastPayload)

		case "event", "log", "cachedevent": // Types requiring specific client processing
			// --- Start Specific Client Processing ---
			clusterName := msg.ClusterName
			messageType := msg.Type
			logger.Log.Debug("Processing message for specific clients", zap.String("type", messageType), zap.String("cluster", clusterName))

			// Cache "event" type messages before potential sending/queueing
			if messageType == "event" {
				s.eventCacheMutex.Lock()
				clusterEvents := s.eventCache[msg.ClusterName]
				if len(clusterEvents) >= 1000 {
					clusterEvents = clusterEvents[1:]
				}
				cachedMsg := msg
				cachedMsg.Type = "cachedevent"
				s.eventCache[msg.ClusterName] = append(clusterEvents, cachedMsg)
				s.eventCacheMutex.Unlock()
			}

			s.clientsMapMutex.RLock()
			clientCount := len(s.clients)
			var sentCount, failedCount int
			// Define a struct to store both client and the message that needs queueing
			type clientMessagePair struct {
				client  *Client
				message utils.Message
			}
			clientsNeedsQueueing := []clientMessagePair{}

			for client := range s.clients {
				client.watchEventslistMutex.RLock()
				shouldSend := false
				needsQueueing := false
				msgCopy := msg

				// Read session IDs under lock
				client.stateMutex.RLock()
				cLogSessionId := client.logSessionId
				cEventSessionId := client.eventSessionId
				client.stateMutex.RUnlock()

				switch messageType {
				case "log":
					shouldSend = cLogSessionId == msgCopy.SessionID
				case "event":
					shouldSend = client.watchEventslist[clusterName]
					if shouldSend { // Only set session ID if we intend to send/queue
						msgCopy.SessionID = cEventSessionId // Use locked value
						client.stateMutex.Lock()            // Use full lock for sendingCached write access check
						sendingCached := client.sendingCached
						client.stateMutex.Unlock()
						if sendingCached {
							needsQueueing = true
							shouldSend = false
							// Store both client and the specific message that needs queueing
							clientsNeedsQueueing = append(clientsNeedsQueueing, clientMessagePair{client: client, message: msgCopy})
						}
					}
				case "cachedevent":
					shouldSend = client.watchEventslist[clusterName] && cEventSessionId == msgCopy.SessionID
				}

				client.watchEventslistMutex.RUnlock()

				if needsQueueing {
					// We now track client-message pairs directly, so no need to add them here
				} else if shouldSend {
					if s.sendToClient(client, msgCopy) {
						sentCount++
					} else {
						failedCount++
						// Error handled in sendToClient, just update count and continue outer loop
						continue // Go to the next client in the outer loop
					}
				}
			} // End client loop
			s.clientsMapMutex.RUnlock()

			// Handle queueing outside the clients map RLock
			if len(clientsNeedsQueueing) > 0 {
				s.clientQueueMutex.Lock()
				for _, pair := range clientsNeedsQueueing {
					// Ensure client still exists before queueing (might have disconnected between loops)
					s.clientsMapMutex.RLock()
					_, clientExists := s.clients[pair.client]
					s.clientsMapMutex.RUnlock()
					if !clientExists {
						continue
					}

					s.clientQueue[pair.client] = true
					pair.client.stateMutex.Lock()
					// Add only the specific message that was marked for queueing
					pair.client.eventQueue = append(pair.client.eventQueue, pair.message)
					pair.client.stateMutex.Unlock()
				}
				s.clientQueueMutex.Unlock()
			}

			// Log summary
			if sentCount > 0 || failedCount > 0 {
				logLevel := logger.Log.Debug
				if failedCount > 0 {
					logLevel = logger.Log.Info
				}
				logLevel("Specific message processing summary",
					zap.String("type", messageType),
					zap.String("cluster", clusterName),
					zap.Int("sentTo", sentCount),
					zap.Int("failed", failedCount),
					zap.Int("totalClientsChecked", clientCount))
			}
			// --- End Specific Client Processing ---

		default:
			// Handle unknown message types
			logger.Log.Warn("Received message with unhandled type", zap.String("type", msg.Type))
		}
	}
}

// sendToClient sends a message payload to a specific client and handles errors.
// It returns true if the message was sent successfully, false otherwise.
func (s *Server) sendToClient(client *Client, payload interface{}) bool {
	err := client.conn.WriteJSON(payload)
	if err != nil {
		// Log appropriately based on message type if possible (or pass type info)
		// For simplicity here, we'll use a generic warning.
		logger.Log.Warn("Failed to send message to client, disconnecting", zap.Error(err))
		client.conn.Close()

		// Remove client from clients map and client queue (using sequential locks)
		s.clientsMapMutex.Lock()
		delete(s.clients, client)
		s.clientsMapMutex.Unlock()

		s.clientQueueMutex.Lock()
		delete(s.clientQueue, client)
		s.clientQueueMutex.Unlock()
		return false // Indicate failure
	}
	return true // Indicate success
}

// broadcastToAllClients sends a payload to all connected clients.
// This helper assumes the caller doesn't need specific per-client logic.
func (s *Server) broadcastToAllClients(payload interface{}) {
	s.clientsMapMutex.RLock()                         // Use RLock as we only read the client map
	clientsCopy := make([]*Client, 0, len(s.clients)) // Copy client pointers
	for client := range s.clients {
		clientsCopy = append(clientsCopy, client)
	}
	s.clientsMapMutex.RUnlock()

	failedCount := 0
	clientCount := len(clientsCopy)

	for _, client := range clientsCopy {
		err := client.conn.WriteJSON(payload)
		if err != nil {
			failedCount++
			logger.Log.Warn("Failed to send broadcast payload to client", zap.Error(err))
			client.conn.Close()

			// Remove client from the maps (needs write locks)
			s.clientsMapMutex.Lock()
			delete(s.clients, client)
			s.clientsMapMutex.Unlock()

			s.clientQueueMutex.Lock()
			delete(s.clientQueue, client)
			s.clientQueueMutex.Unlock()
		}
	}

	if failedCount > 0 {
		logger.Log.Info("Broadcast to all clients completed with errors",
			zap.Int("totalClientsAttempted", clientCount),
			zap.Int("failedClients", failedCount))
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

	// Add the cluster if it doesn't exist (with lock)
	s.clustersMutex.Lock()
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
		s.clustersMutex.Unlock() // Unlock before broadcasting
		s.broadcastClusters()
	} else {
		logger.Log.Debug("Cluster already in list", zap.String("cluster", clusterName))
		s.clustersMutex.Unlock()
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

	// Remove the cluster from the list if it exists (with lock)
	s.clustersMutex.Lock()
	found := false
	for i, name := range s.clusters {
		if name == clusterName {
			s.clusters = append(s.clusters[:i], s.clusters[i+1:]...)
			logger.Log.Info("Removed cluster from list", zap.String("cluster", clusterName))
			found = true
			// Also remove from conditions map
			s.clusterConditionsMutex.Lock() // Lock before deleting
			if _, exists := s.clusterConditions[clusterName]; exists {
				delete(s.clusterConditions, clusterName)
				logger.Log.Info("Removed cluster conditions for", zap.String("cluster", clusterName))
			}
			s.clusterConditionsMutex.Unlock() // Unlock after deleting
			break
		}
	}
	s.clustersMutex.Unlock()

	if found {
		s.broadcastClusters()
	}
}

// Helper function to broadcast the current list of clusters to all clients
func (s *Server) broadcastClusters() {
	// Read clusters with RLock
	s.clustersMutex.RLock()
	clustersToSend := make([]string, len(s.clusters))
	copy(clustersToSend, s.clusters)
	s.clustersMutex.RUnlock()

	// Send a single message to the broadcast channel for handleMessages to process
	s.broadcast <- utils.Message{
		Type:     "clustersListUpdate", // New type
		Clusters: clustersToSend,
	}
	logger.Log.Debug("Sent clustersListUpdate to broadcast channel", zap.Int("clusterCount", len(clustersToSend)))
}

// Function to handle cluster conditions using the object from the informer
func (s *Server) updateConditions(obj interface{}) {
	logger.Log.Debug("updateConditions called with object")

	if obj == nil {
		return
	}

	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	clusterName := unstructuredObj.GetName()
	logger.Log.Debug("Processing conditions for cluster", zap.String("cluster", clusterName))

	// Always ensure the cluster has an entry in the conditions map
	s.clusterConditionsMutex.Lock() // Lock before write access
	if _, exists := s.clusterConditions[clusterName]; !exists {
		s.clusterConditions[clusterName] = []map[string]interface{}{}
		logger.Log.Debug("Initialized empty conditions for cluster", zap.String("cluster", clusterName))
	}
	s.clusterConditionsMutex.Unlock() // Unlock after potential write

	// Extract conditions from the object if available
	status, found, err := unstructured.NestedMap(unstructuredObj.Object, "status")
	if err != nil {
		logger.Log.Error("Error getting status", zap.Error(err))
		s.broadcastConditions()
		return
	}

	if !found {
		logger.Log.Debug("No status found in object for cluster", zap.String("cluster", clusterName))
		s.broadcastConditions()
		return
	}

	conditions, found, err := unstructured.NestedSlice(status, "conditions")
	if err != nil {
		logger.Log.Error("Error getting conditions", zap.Error(err))
		s.broadcastConditions()
		return
	}

	if !found || len(conditions) == 0 {
		logger.Log.Debug("No conditions found in status for cluster", zap.String("cluster", clusterName))
		s.broadcastConditions()
		return
	}

	var conditionsList []map[string]interface{}
	for _, condition := range conditions {
		if condMap, ok := condition.(map[string]interface{}); ok {
			conditionsList = append(conditionsList, condMap)
		}
	}

	// Only update if we found actual conditions
	if len(conditionsList) > 0 {
		s.clusterConditionsMutex.Lock() // Lock before write access
		s.clusterConditions[clusterName] = conditionsList
		s.clusterConditionsMutex.Unlock() // Unlock after write
		logger.Log.Debug("Updated conditions for cluster", zap.String("cluster", clusterName))
	}

	// Always broadcast the conditions to all connected clients
	s.broadcastConditions()
}

// Helper function to broadcast the current conditions to all clients
func (s *Server) broadcastConditions() {
	// NOTE: clusterConditions map access doesn't need separate lock here due to serial watcher processing
	// Create a deep copy to avoid race conditions if the map is modified elsewhere concurrently
	// Although unlikely with current watcher model, it's safer.
	s.clusterConditionsMutex.RLock() // RLock for read access
	conditionsToSend := make(map[string][]map[string]interface{})
	for k, v := range s.clusterConditions {
		// Assuming v (the slice of maps) doesn't need deep copy for broadcast, only the top level map
		conditionsToSend[k] = v
	}
	s.clusterConditionsMutex.RUnlock() // RUnlock after read

	// Send a single message to the broadcast channel for handleMessages to process
	s.broadcast <- utils.Message{
		Type:       "conditionsUpdate", // New type
		Conditions: conditionsToSend,
	}
	logger.Log.Debug("Sent conditionsUpdate to broadcast channel", zap.Int("clusterCount", len(conditionsToSend)))
}

// sendCachedEvents sends cached events for a cluster to a specific client
func (s *Server) sendCachedEvents(client *Client, clusterName string) {
	s.eventCacheMutex.RLock()
	events, exists := s.eventCache[clusterName]
	eventCount := len(events)
	s.eventCacheMutex.RUnlock()

	// Read eventSessionId under lock
	client.stateMutex.RLock()
	cEventSessionId := client.eventSessionId
	client.stateMutex.RUnlock()

	if !exists || eventCount == 0 {
		logger.Log.Debug("No cached events to send",
			zap.String("cluster", clusterName),
			zap.String("sessionId", cEventSessionId)) // Use locked value
		return
	}

	logger.Log.Debug("Sending cached events",
		zap.String("cluster", clusterName),
		zap.String("sessionId", cEventSessionId), // Use locked value
		zap.Int("eventCount", eventCount))

	// Set sendingCached flag to true with client state lock
	client.stateMutex.Lock()
	client.sendingCached = true
	client.stateMutex.Unlock()

	// Use defer to ensure the flag gets reset even if there's a panic
	defer func() {
		client.stateMutex.Lock()
		client.sendingCached = false
		client.stateMutex.Unlock()

		logger.Log.Debug("Completed sending cached events",
			zap.String("cluster", clusterName),
			zap.String("sessionId", cEventSessionId)) // Use locked value
	}()

	// Send cached events
	for _, event := range events {
		event.SessionID = cEventSessionId // Use locked value
		// Send to broadcast channel instead of directly to client
		s.broadcast <- event
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
	s.clustersMutex.RLock()
	clustersCopy := make([]string, len(s.clusters))
	copy(clustersCopy, s.clusters)
	s.clustersMutex.RUnlock()

	tmpl, _ := template.ParseFiles("templates/index.html")
	tmpl.Execute(w, clustersCopy)
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

	// Check if the cluster exists in our tracked clusters list (with RLock)
	s.clustersMutex.RLock()
	clusterExists := false
	for _, name := range s.clusters {
		if name == clusterName {
			clusterExists = true
			break
		}
	}
	s.clustersMutex.RUnlock()

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
			// Only include metrics that are in the allowedMetrics map
			//fmt.Println("this is the metric name", mf.GetName())
			if s.allowedMetrics[mf.GetName()] {
				//fmt.Println("this is the true metric name", mf.GetName())
				result = append(result, prom2json.NewFamily(mf))
			}
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
