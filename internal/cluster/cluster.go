package cluster

import (
	"context"
	"os"
	"time"

	"cod/internal/logger"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

func StartClusterWatcher(ctx context.Context, dynamicClient dynamic.Interface,
	addCluster func(obj interface{}),
	deleteCluster func(obj interface{}),
	updateCondition func(obj interface{})) {

	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		logger.Log.Fatal("WATCH_NAMESPACE environment variable not set - cluster operations disabled")
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "couchbase.com",
		Version:  "v2",
		Resource: "couchbaseclusters",
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 30*time.Second, namespace, nil)
	clusterInformer := factory.ForResource(gvr).Informer()

	logger.Log.Info("Starting cluster watcher", zap.String("namespace", namespace))

	clusterInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			unstructuredObj, ok := obj.(*unstructured.Unstructured)
			if !ok {
				logger.Log.Error("AddFunc received unknown object type",
					zap.String("namespace", namespace),
					zap.String("objectType", "unknown"))
				return
			}

			logger.Log.Info("Cluster added",
				zap.String("name", unstructuredObj.GetName()),
				zap.String("namespace", namespace))

			addCluster(obj)
			updateCondition(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			unstructuredObj, ok := newObj.(*unstructured.Unstructured)
			if !ok {
				logger.Log.Error("UpdateFunc received unknown object type",
					zap.String("namespace", namespace),
					zap.String("objectType", "unknown"))
				return
			}

			// Only log at Info level if it's being deleted, otherwise Debug
			if unstructuredObj.GetDeletionTimestamp() != nil {
				logger.Log.Info("Cluster marked for deletion",
					zap.String("name", unstructuredObj.GetName()),
					zap.String("namespace", namespace),
					zap.Time("deletionTimestamp", unstructuredObj.GetDeletionTimestamp().Time))
			} else {
				logger.Log.Debug("Cluster updated",
					zap.String("name", unstructuredObj.GetName()),
					zap.String("namespace", namespace))
			}

			updateCondition(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			// Handle direct deletion
			if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
				logger.Log.Info("Cluster deleted",
					zap.String("name", unstructuredObj.GetName()),
					zap.String("namespace", namespace))
				deleteCluster(obj)
				return
			}

			// Handle tombstone deletion
			tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
			if !ok {
				logger.Log.Error("DeleteFunc received unknown object type",
					zap.String("namespace", namespace),
					zap.String("objectType", "unknown"))
				return
			}

			unstructuredObj, ok := tombstone.Obj.(*unstructured.Unstructured)
			if !ok {
				logger.Log.Error("Tombstone contains unknown object type",
					zap.String("namespace", namespace),
					zap.String("objectType", "tombstone"))
				return
			}

			logger.Log.Info("Cluster deleted (from tombstone)",
				zap.String("name", unstructuredObj.GetName()),
				zap.String("namespace", namespace))
			deleteCluster(obj)
		},
	})

	factory.Start(ctx.Done())
	sync := cache.WaitForCacheSync(ctx.Done(), clusterInformer.HasSynced)
	if !sync {
		logger.Log.Error("Failed to sync cluster informer cache",
			zap.String("namespace", namespace),
			zap.String("resource", "couchbaseclusters"))
	}
}

// LoadClusterConditions fetches all existing CouchbaseCluster objects and updates their conditions
// This should be called when a new client connects to immediately get all cluster conditions
func LoadClusterConditions(dynamicClient dynamic.Interface, updateCondition func(obj interface{})) error {
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		logger.Log.Warn("WATCH_NAMESPACE not set - cannot load cluster conditions")
		return nil
	}

	// Create the CouchbaseCluster GVR
	gvr := schema.GroupVersionResource{
		Group:    "couchbase.com",
		Version:  "v2",
		Resource: "couchbaseclusters",
	}

	// List all CouchbaseCluster objects in the namespace
	clusters, err := dynamicClient.Resource(gvr).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		logger.Log.Error("Failed to list CouchbaseCluster objects",
			zap.Error(err),
			zap.String("namespace", namespace),
			zap.String("resource", "couchbaseclusters"))
		return err
	}

	logger.Log.Info("Loading cluster conditions",
		zap.String("namespace", namespace),
		zap.Int("clusterCount", len(clusters.Items)))

	// Process each cluster to update conditions
	for _, cluster := range clusters.Items {
		updateCondition(&cluster)
	}

	return nil
}
