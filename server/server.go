package server


import (
    "context"
    "encoding/json"
    "html/template"
    "log"
    "net/http"
    "os"
    "sync"

    "cod/events"
    "cod/utils"
    "cod/debug"

    "github.com/gorilla/websocket"
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)


type Client struct {
    conn      *websocket.Conn
    watchlist map[string]bool
}

type Server struct {
	clusters	  []string
    upgrader      websocket.Upgrader
    clients       map[*Client]bool
    watchers      map[string]context.CancelFunc
    broadcast     chan events.EventMessage
    clientsMutex  sync.Mutex
    watchersMutex sync.Mutex
    clientset     *kubernetes.Clientset
    dynamicClient dynamic.Interface
}

func NewServer() *Server {
    return &Server{
        upgrader:  websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024},
        clients:   make(map[*Client]bool),
        watchers:  make(map[string]context.CancelFunc),
        broadcast: make(chan events.EventMessage),
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
    go utils.StartClusterWatcher(ctx,s.dynamicClient, s.updateClusters)

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

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
    debug.Println("Handling ws connection")
    ws, err := s.upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Fatalf("Failed to upgrade to websocket: %v", err)
    }
    defer ws.Close()

    client := &Client{conn: ws, watchlist: make(map[string]bool)}
    s.clientsMutex.Lock()
    s.clients[client] = true
    s.clientsMutex.Unlock()

    defer func() {
        s.clientsMutex.Lock()
        delete(s.clients, client)
        s.clientsMutex.Unlock()
        s.cleanupWatchers()
    }()

    for {
        _, message, err := ws.ReadMessage()
        if err != nil {
            log.Printf("Error reading message: %v", err)
            break
        }

        var request struct {
            Clusters []string `json:"clusters"`
        }
        if err := json.Unmarshal(message, &request); err != nil {
            log.Printf("Error unmarshalling message: %v", err)
            continue
        }

        s.clientsMutex.Lock()
        client.watchlist = make(map[string]bool)
        for _, cluster := range request.Clusters {
            client.watchlist[cluster] = true
            s.startWatcher(cluster) // Start watcher for new cluster :- it handles if cluster has an existing watcher
        }
        s.clientsMutex.Unlock()

        s.cleanupWatchers()
    }
}

func (s *Server) startWatcher(clusterName string) {
    s.watchersMutex.Lock()
    defer s.watchersMutex.Unlock()

    if _, exists := s.watchers[clusterName]; exists {
        return
    }

    ctx, cancel := context.WithCancel(context.Background())
    s.watchers[clusterName] = cancel
    go events.StartEventWatcher(ctx, s.clientset, s.dynamicClient, clusterName, s.broadcast)
}

func (s *Server) cleanupWatchers() {
    s.watchersMutex.Lock()
    defer s.watchersMutex.Unlock()

    activeClusters := make(map[string]bool)
    s.clientsMutex.Lock()
    for client := range s.clients {
        for cluster := range client.watchlist {
            activeClusters[cluster] = true
        }
    }
    s.clientsMutex.Unlock()

    for cluster, cancel := range s.watchers {
        if !activeClusters[cluster] {
            cancel()
            delete(s.watchers, cluster)
        }
    }
}

func (s *Server) handleMessages() {
    for {
        msg := <-s.broadcast
        //debug.Println("Broadcasting message:", msg)
        s.clientsMutex.Lock()
        for client := range s.clients {
            if client.watchlist[msg.ClusterName] {
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