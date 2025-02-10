package events

import (
    "context"
    "fmt"
    "os"
    "time"

    "cod/debug"

    v1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/informers"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/cache"
)

var clusterName = "cb_example" 

func StartEventWatcher(ctx context.Context, clientset *kubernetes.Clientset, dynamicClient dynamic.Interface) {
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
            if !ok || !isRelevantEvent(event, clientset, dynamicClient) {
                return
            }
            fmt.Printf("New event: %s - %s\n", event.Name, event.Message)
        },
        UpdateFunc: func(oldObj, newObj interface{}) {
            event, ok := newObj.(*v1.Event)
            if !ok || !isRelevantEvent(event, clientset, dynamicClient) {
                return
            }
            fmt.Printf("Updated event: %s - %s\n", event.Name, event.Message)
        },
        DeleteFunc: func(obj interface{}) {
            event, ok := obj.(*v1.Event)
            if !ok || !isRelevantEvent(event, clientset, dynamicClient) {
                return
            }
            fmt.Printf("Deleted event: %s\n", event.Name)
        },
    })

    factory.Start(ctx.Done())
    cache.WaitForCacheSync(ctx.Done(), eventInformer.HasSynced)
}

// isRelevantEvent checks if the event is relevant based on labels
func isRelevantEvent(event *v1.Event, clientset *kubernetes.Clientset, dynamicClient dynamic.Interface) bool {
    debug.Println("-------------------------------------------------------")
    debug.Println("Event kind:", event.InvolvedObject.Kind)
    debug.Println("Event objexct name:", event.InvolvedObject.Name)
    debug.Println("Event name:", event.Name)
    debug.Println("Event message:", event.Message)

    if event.InvolvedObject.Kind == "Pod" {
        labels := getObjectLabels(clientset, dynamicClient, event.InvolvedObject)

        if labels["couchbase_cluster"] == clusterName || labels["app"] == "couchbase-operator" {
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

// getObjectLabels retrieves labels of an involved object
func getObjectLabels(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, obj v1.ObjectReference) map[string]string {
    switch obj.Kind {
    case "Pod":
        pod, err := clientset.CoreV1().Pods(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
        if err != nil {
            debug.Println("Error fetching pod:", err)
            return nil
        }
        return pod.Labels
    case "CouchbaseCluster":
        gvr := schema.GroupVersionResource{
            Group:    "couchbase.com", 
            Version:  "v2",            
            Resource: "couchbaseclusters",
        }
        resource, err := dynamicClient.Resource(gvr).Namespace(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
        if err != nil {
            debug.Println("Error fetching CouchbaseCluster:", err)
            return nil
        }
        return resource.GetLabels()
    default:
        debug.Println("Unsupported kind:", obj.Kind)
        return nil
    }
}




