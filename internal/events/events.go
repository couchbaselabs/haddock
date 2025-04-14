package events

import (
	"context"
	"os"
	"time"

	"cod/internal/logger"
	"cod/internal/utils"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

func StartEventWatcher(ctx context.Context, clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, clusterName string, broadcast chan utils.Message) {
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		logger.Log.Fatal("WATCH_NAMESPACE environment variable not set - event watching disabled",
			zap.String("cluster", clusterName))
		return
	}

	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 30*time.Second, informers.WithNamespace(namespace))
	eventInformer := factory.Core().V1().Events().Informer()

	logger.Log.Info("Starting event watcher",
		zap.String("namespace", namespace),
		zap.String("cluster", clusterName))

	eventInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event, ok := obj.(*v1.Event)
			if !ok || !isRelevantEvent(event, clientset, dynamicClient, clusterName) {
				return
			}

			logger.Log.Debug("Broadcasting event",
				zap.String("kind", event.InvolvedObject.Kind),
				zap.String("name", event.InvolvedObject.Name),
				zap.String("message", event.Message),
				zap.String("cluster", clusterName))

			msg := utils.Message{
				Type:        "event",
				ClusterName: clusterName,
				Name:        event.Name,
				Message:     event.Message,
				Kind:        event.InvolvedObject.Kind,
				ObjectName:  event.InvolvedObject.Name,
			}
			broadcast <- msg
		},
	})

	factory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), eventInformer.HasSynced)
}

// isRelevantEvent checks if the event is relevant based on labels
func isRelevantEvent(event *v1.Event, clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, clusterName string) bool {
	// Early return for CouchbaseCluster events
	if event.InvolvedObject.Kind == "CouchbaseCluster" && event.InvolvedObject.Name == clusterName {
		return true
	}

	// Early return for non-Pod events
	if event.InvolvedObject.Kind != "Pod" {
		return false
	}

	// Check Pod labels
	labels := utils.GetPodLabels(clientset, dynamicClient, event.InvolvedObject)
	if labels == nil {
		return false
	}

	// Check if pod belongs to the cluster or is the operator
	return labels["couchbase_cluster"] == clusterName || labels["app"] == "couchbase-operator"
}

// GetInitialEvents retrieves existing events for a cluster
func GetInitialEvents(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, clusterName string) []utils.Message {
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		logger.Log.Warn("WATCH_NAMESPACE not set - cannot retrieve initial events",
			zap.String("cluster", clusterName))
		return nil
	}

	var initialEvents []utils.Message

	// List existing events
	eventList, err := clientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logger.Log.Error("Failed to list events",
			zap.Error(err),
			zap.String("namespace", namespace),
			zap.String("cluster", clusterName))
		return nil
	}

	logger.Log.Info("Retrieving initial events",
		zap.String("namespace", namespace),
		zap.String("cluster", clusterName),
		zap.Int("totalEvents", len(eventList.Items)))

	// Convert to Message objects
	for _, event := range eventList.Items {
		if isRelevantEvent(&event, clientset, dynamicClient, clusterName) {
			msg := utils.Message{
				Type:        "cachedevent",
				ClusterName: clusterName,
				Name:        event.Name,
				Message:     event.Message,
				Kind:        event.InvolvedObject.Kind,
				ObjectName:  event.InvolvedObject.Name,
			}
			initialEvents = append(initialEvents, msg)
		}
	}

	logger.Log.Info("Initial events retrieved",
		zap.String("cluster", clusterName),
		zap.Int("relevantEvents", len(initialEvents)))

	return initialEvents
}
