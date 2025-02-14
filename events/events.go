package events

import (
    "context"
    "fmt"
    "os"
    "time"


    "cod/debug"
    "cod/utils"

    v1 "k8s.io/api/core/v1"
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/informers"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/cache"
)



func StartEventWatcher(ctx context.Context, clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, clusterName string) {
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
            if !ok || !isRelevantEvent(event, clientset, dynamicClient,clusterName) {
                return
            }
            fmt.Printf("New event: %s - %s\n", event.Name, event.Message)
        },
        UpdateFunc: func(oldObj, newObj interface{}) {
            event, ok := newObj.(*v1.Event)
            if !ok || !isRelevantEvent(event, clientset, dynamicClient,clusterName) {
                return
            }
            fmt.Printf("Updated event: %s - %s\n", event.Name, event.Message)
        },
        DeleteFunc: func(obj interface{}) {
            event, ok := obj.(*v1.Event)
            if !ok || !isRelevantEvent(event, clientset, dynamicClient,clusterName) {
                return
            }
            fmt.Printf("Deleted event: %s\n", event.Name)
        },
    })

    factory.Start(ctx.Done())
    cache.WaitForCacheSync(ctx.Done(), eventInformer.HasSynced)
}

// isRelevantEvent checks if the event is relevant based on labels
func isRelevantEvent(event *v1.Event, clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, clusterName string) bool {
    debug.Println("-------------------------------------------------------")
    debug.Println("Event kind:", event.InvolvedObject.Kind)
    debug.Println("Event object name:", event.InvolvedObject.Name)
    debug.Println("Event name:", event.Name)
    debug.Println("Event message:", event.Message)

    if event.InvolvedObject.Kind == "Pod" {
        labels := utils.GetObjectLabels(clientset, dynamicClient, event.InvolvedObject)
        

        if labels["couchbase_cluster"] == clusterName || labels["app"] == "couchbase-operator" {
            debug.Println("Relevant pod event")
            return true
        }
    }

    if event.InvolvedObject.Kind == "CouchbaseCluster" && event.InvolvedObject.Name == clusterName {
        debug.Println("Relevant cluster event")
        return true
    }

    debug.Println("Irrelevant event")
    return false
}



