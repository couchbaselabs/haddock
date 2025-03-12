package events

import (
	"context"
	//"fmt"
	"os"
	"time"

	"cod/logger"
	"cod/utils"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	//"k8s.io/apimachinery/pkg/runtime"
	//"k8s.io/apimachinery/pkg/watch"
)

func StartEventWatcher(ctx context.Context, clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, clusterName string, broadcast chan utils.Message) {
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		logger.Log.Warn("WATCH_NAMESPACE environment variable not set")
		return
	}

	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 30*time.Second, informers.WithNamespace(namespace))
	eventInformer := factory.Core().V1().Events().Informer()

	eventInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			logger.Log.Debug("Event added")
			event, ok := obj.(*v1.Event)
			if !ok || !isRelevantEvent(event, clientset, dynamicClient, clusterName) {
				return
			}
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
	logger.Log.Debug("Checking event relevance",
		zap.String("kind", event.InvolvedObject.Kind),
		zap.String("objectName", event.InvolvedObject.Name),
		zap.String("eventName", event.Name),
		zap.String("message", event.Message))

	if event.InvolvedObject.Kind == "Pod" {
		labels := utils.GetPodLabels(clientset, dynamicClient, event.InvolvedObject)
		if labels["couchbase_cluster"] == clusterName || labels["app"] == "couchbase-operator" { // app label is used for couchbase-operator
			logger.Log.Debug("Relevant pod event")
			return true
		}
	}

	if event.InvolvedObject.Kind == "CouchbaseCluster" && event.InvolvedObject.Name == clusterName {
		logger.Log.Debug("Relevant cluster event")
		return true
	}

	logger.Log.Debug("Irrelevant event")
	return false
}

// GetInitialEvents retrieves existing events for a cluster
func GetInitialEvents(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, clusterName string) []utils.Message {
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		return nil
	}

	var initialEvents []utils.Message

	// List existing events
	eventList, err := clientset.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil
	}

	// Convert to Message objects
	for _, event := range eventList.Items {
		if isRelevantEvent(&event, clientset, dynamicClient, clusterName) {
			msg := utils.Message{
				Type:        "event",
				ClusterName: clusterName,
				Name:        event.Name,
				Message:     event.Message,
				Kind:        event.InvolvedObject.Kind,
				ObjectName:  event.InvolvedObject.Name,
			}
			initialEvents = append(initialEvents, msg)
		}
	}

	return initialEvents
}
