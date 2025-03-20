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
		logger.Log.Warn("WATCH_NAMESPACE environment variable not set")
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    "couchbase.com",
		Version:  "v2",
		Resource: "couchbaseclusters",
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dynamicClient, 30*time.Second, namespace, nil)
	clusterInformer := factory.ForResource(gvr).Informer()

	clusterInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			logger.Log.Debug("Cluster added event received")
			if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
				logger.Log.Info("Cluster added", zap.String("name", unstructuredObj.GetName()))
			}
			addCluster(obj)
			updateCondition(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			logger.Log.Debug("Cluster updated event received")
			if unstructuredObj, ok := newObj.(*unstructured.Unstructured); ok {
				logger.Log.Info("Cluster updated", zap.String("name", unstructuredObj.GetName()))
				if unstructuredObj.GetDeletionTimestamp() != nil {
					logger.Log.Debug("Cluster is being deleted (UpdateFunc)", zap.String("name", unstructuredObj.GetName()))
				}
			}
			updateCondition(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			logger.Log.Debug("Cluster deleted event received")
			if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
				logger.Log.Info("Cluster deleted", zap.String("name", unstructuredObj.GetName()))
			} else if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				if unstructuredObj, ok := tombstone.Obj.(*unstructured.Unstructured); ok {
					logger.Log.Debug("Deleted cluster (from tombstone)", zap.String("name", unstructuredObj.GetName()))
				} else {
					logger.Log.Warn("Tombstone contains unknown object type")
				}
			} else {
				logger.Log.Warn("DeleteFunc received unknown object type")
			}
			deleteCluster(obj)
		},
	})

	factory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), clusterInformer.HasSynced)
}

// LoadClusterConditions fetches all existing CouchbaseCluster objects and updates their conditions
// This should be called when a new client connects to immediately get all cluster conditions
func LoadClusterConditions(dynamicClient dynamic.Interface, updateCondition func(obj interface{})) error {
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
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
		logger.Log.Error("Error listing CouchbaseCluster objects", zap.Error(err))
		return err
	}

	// Process each cluster to update conditions
	for _, cluster := range clusters.Items {
		logger.Log.Debug("Loading conditions for cluster", zap.String("name", cluster.GetName()))
		updateCondition(&cluster)
	}

	return nil
}
