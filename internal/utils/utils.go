package utils

import (
	"context"

	"cod/internal/logger"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type Message struct {
	Type        string                              `json:"type"`
	ClusterName string                              `json:"clusterName,omitempty"`
	Name        string                              `json:"name,omitempty"`
	Message     string                              `json:"message,omitempty"`
	Kind        string                              `json:"kind,omitempty"`
	ObjectName  string                              `json:"objectName,omitempty"`
	SessionID   string                              `json:"sessionId,omitempty"`
	Clusters    []string                            `json:"clusters,omitempty"`
	Conditions  map[string][]map[string]interface{} `json:"conditions,omitempty"`
}

// GetPodLabels retrieves labels of an involved pod
func GetPodLabels(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, obj v1.ObjectReference) map[string]string {
	// Early return for non-Pod objects
	if obj.Kind != "Pod" {
		return nil
	}

	pod, err := clientset.CoreV1().Pods(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
	if err != nil {
		// Check if the error is a Kubernetes API "Not Found" error
		if errors.IsNotFound(err) {
			// Log at Debug level as this is expected for deleted pods
			logger.Log.Debug("Pod not found when fetching labels (likely deleted)",
				zap.String("name", obj.Name),
				zap.String("namespace", obj.Namespace))
		} else {
			// Log other errors as actual errors
			logger.Log.Error("Failed to fetch pod labels",
				zap.Error(err),
				zap.String("name", obj.Name),
				zap.String("namespace", obj.Namespace))
		}
		return nil // Return nil in both error cases
	}

	return pod.Labels
}
