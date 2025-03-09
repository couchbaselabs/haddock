package utils

import (
	"context"

	"cod/debug"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
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
