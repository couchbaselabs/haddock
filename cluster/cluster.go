package cluster

import (
	"context"
	"log"
	"os"
	"time"

	"cod/debug"

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
		log.Println("WATCH_NAMESPACE environment variable not set")
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
			debug.Println("Cluster added event received")
			if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
				debug.Println("Cluster added:", unstructuredObj.GetName())
			}
			addCluster(obj)
			updateCondition(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			debug.Println("Cluster updated event received")
			if unstructuredObj, ok := newObj.(*unstructured.Unstructured); ok {
				debug.Println("Cluster updated:", unstructuredObj.GetName())
				// Check if this is a deletion update (has deletion timestamp)
				if unstructuredObj.GetDeletionTimestamp() != nil {
					debug.Println("Cluster is being deleted (UpdateFunc):", unstructuredObj.GetName())
				}
			}
			updateCondition(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			debug.Println("Cluster deleted event received")
			// Try to get details from the object
			if unstructuredObj, ok := obj.(*unstructured.Unstructured); ok {
				debug.Println("Cluster deleted:", unstructuredObj.GetName())
			} else if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				debug.Println("DeleteFunc received tombstone object")
				if unstructuredObj, ok := tombstone.Obj.(*unstructured.Unstructured); ok {
					debug.Println("Deleted cluster (from tombstone):", unstructuredObj.GetName())
				} else {
					debug.Println("Tombstone contains unknown object type")
				}
			} else {
				debug.Println("DeleteFunc received unknown object type")
			}
			deleteCluster(obj)
		},
	})

	factory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), clusterInformer.HasSynced)
}
