package main

import (
    "context"
    "log"
	"os"

    "cod/events"
	"cod/utils"


    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

func main() {
    config, err := rest.InClusterConfig()
    if err != nil {
        log.Fatalf("Failed to get in-cluster config: %v", err)
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        log.Fatalf("Failed to create Kubernetes client: %v", err)
    }

    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        log.Fatalf("Failed to create dynamic client: %v", err)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    namespace := os.Getenv("WATCH_NAMESPACE")
    if namespace == "" {
        log.Fatalf("WATCH_NAMESPACE environment variable not set")
    }

    clusters, err := utils.GetCouchbaseClusters(dynamicClient, namespace)
    if err != nil {
        log.Fatalf("Error fetching Couchbase clusters: %v", err)
    }

    if len(clusters) == 0 {
        log.Fatalf("No Couchbase clusters found")
    }

    clusterName := utils.SelectCluster(clusters)

    events.StartEventWatcher(ctx, clientset, dynamicClient, clusterName)

    // Block forever (or until a signal is received)
    select {}
}