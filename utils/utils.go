package utils

import (
	"context"
	"log"
	"os"
	"time"

	"cod/debug"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Message struct {
	Type        string `json:"type"`
	ClusterName string `json:"clusterName"`
	Name        string `json:"name"`
	Message     string `json:"message"`
	Kind        string `json:"kind"`
	ObjectName  string `json:"objectName"`
	SessionID   string `json:"sessionId,omitempty"`
}

// getPodLabels retrieves labels of an involved pod
func GetPodLabels(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, obj v1.ObjectReference) map[string]string {
	if obj.Kind == "Pod" {
		debug.Println("Fetching labels for pod:", obj.Name)
		pod, err := clientset.CoreV1().Pods(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
		if err != nil {
			debug.Println("Error fetching pod:", err)
			return nil
		}
		return pod.Labels
	} else {
		debug.Println("Unsupported kind:", obj.Kind)
		return nil
	}
}

// func GetObjectLabels(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, obj v1.ObjectReference) map[string]string {
//     switch obj.Kind {
//     case "Pod":
//         pod, err := clientset.CoreV1().Pods(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
//         if err != nil {
//             debug.Println("Error fetching pod:", err)
//             return nil
//         }
//         return pod.Labels
//     case "CouchbaseCluster":
//         gvr := schema.GroupVersionResource{
//             Group:    "couchbase.com",
//             Version:  "v2",
//             Resource: "couchbaseclusters",
//         }
//         resource, err := dynamicClient.Resource(gvr).Namespace(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
//         if err != nil {
//             debug.Println("Error fetching CouchbaseCluster:", err)
//             return nil
//         }
//         return resource.GetLabels()
//     default:
//         debug.Println("Unsupported kind:", obj.Kind)
//         return nil
//     }
// }

func StartClusterWatcher(ctx context.Context, dynamicClient dynamic.Interface, updateClusters func()) {
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
			updateClusters()
		},
		// UpdateFunc: func(oldObj, newObj interface{}) {
		//     updateClusters()
		// },
		DeleteFunc: func(obj interface{}) {
			updateClusters()
		},
	})

	factory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), clusterInformer.HasSynced)
}

func GetCouchbaseClusters(dynamicClient dynamic.Interface, namespace string) ([]string, error) {
	gvr := schema.GroupVersionResource{
		Group:    "couchbase.com",
		Version:  "v2",
		Resource: "couchbaseclusters",
	}
	resourceList, err := dynamicClient.Resource(gvr).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var clusters []string
	for _, resource := range resourceList.Items {
		clusters = append(clusters, resource.GetName())
	}
	debug.Println("Found Couchbase clusters:", clusters)
	return clusters, nil
}

// func SelectCluster(clusters []string) string {
//     fmt.Println("Available Couchbase clusters:")
//     for i, cluster := range clusters {
//         fmt.Printf("[%d] %s\n", i+1, cluster)
//     }

//     reader := bufio.NewReader(os.Stdin)
//     for {
//         fmt.Print("Select a cluster by index: ")
//         input, _ := reader.ReadString('\n')
//         index, err := strconv.Atoi(input[:len(input)-1])
//         if err == nil && index >= 1 && index <= len(clusters) {
//             return clusters[index-1]
//         }
//         fmt.Println("Invalid selection, please try again.")
//     }
// }

// func selectCluster(clusters []string) string {
//     fmt.Println("Select a cluster to watch:")
//     for i, cluster := range clusters {
//         fmt.Printf("%d. %s\n", i+1, cluster)
//     }

//     reader := bufio.NewReader(os.Stdin)
//     for {
//         fmt.Print("Enter cluster number: ")
//         text, _ := reader.ReadString('\n')
//         var clusterNum int
//         _, err := fmt.Sscan(text, &clusterNum)
//         if err != nil || clusterNum < 1 || clusterNum > len(clusters) {
//             fmt.Println("Invalid input. Please enter a valid cluster number.")
//             continue
//         }
//         return clusters[clusterNum-1]
//     }
// }
