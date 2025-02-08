package events

import (
	"context"
	"fmt"
	"time"
	"os"
    "cod/debug"

	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	//"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)


var clusterName = "cb_example" 

func StartEventWatcher(ctx context.Context, clientset *kubernetes.Clientset) {
    namespace := os.Getenv("WATCH_NAMESPACE")
    if namespace == "" {
        debug.Println("WATCH_NAMESPACE environment variable not set")
        return
    }

    factory := informers.NewSharedInformerFactoryWithOptions(clientset, 30*time.Second, informers.WithNamespace(namespace))
    eventInformer := factory.Core().V1().Events().Informer()

    eventInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
            event, ok := obj.(*v1.Event)
            if !ok || !isRelevantEvent(event) {
                return
            }
            fmt.Printf("New event: %s - %s\n", event.Name, event.Message)
        },
        UpdateFunc: func(oldObj, newObj interface{}) {
            event, ok := newObj.(*v1.Event)
            if !ok || !isRelevantEvent(event) {
                return
            }
            fmt.Printf("Updated event: %s - %s\n", event.Name, event.Message)
        },
        DeleteFunc: func(obj interface{}) {
            event, ok := obj.(*v1.Event)
            if !ok || !isRelevantEvent(event) {
                return
            }
            fmt.Printf("Deleted event: %s\n", event.Name)
        },
    })

    factory.Start(ctx.Done())
    cache.WaitForCacheSync(ctx.Done(), eventInformer.HasSynced)
}
func isRelevantEvent(event *v1.Event) bool {
    //debug.Println("Event Kind:", event.InvolvedObject.Kind)
    //debug.Println("Event Labels:", event.ObjectMeta.Labels)
    //debug.Println("Event Name:", event.InvolvedObject.Name)

    debug.Println("-------------------------------------------------------")
    debug.Println("Cluster:", event.ObjectMeta.Labels["couchbase_cluster"] )
    if event.InvolvedObject.Kind == "Pod" {
        if event.ObjectMeta.Labels["couchbase_cluster"] == clusterName || event.ObjectMeta.Labels["app"] == "couchbase-operator" {
            debug.Println("Relevant event")
            return true
        }
    }
    if event.InvolvedObject.Kind == "CouchbaseCluster" && event.InvolvedObject.Name == clusterName {
        debug.Println("Relevant event")
        return true
    }
    debug.Println("Irrelevant event")
    return false
}



