package utils

import (
	"context"

	"cod/internal/logger"

	"go.uber.org/zap"
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

// GetPodLabels retrieves labels of an involved pod
func GetPodLabels(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, obj v1.ObjectReference) map[string]string {
	// Early return for non-Pod objects
	if obj.Kind != "Pod" {
		return nil
	}

	pod, err := clientset.CoreV1().Pods(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
	if err != nil {
		logger.Log.Error("Failed to fetch pod labels",
			zap.Error(err),
			zap.String("name", obj.Name),
			zap.String("namespace", obj.Namespace))
		return nil
	}

	return pod.Labels
}
