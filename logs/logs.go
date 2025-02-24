package logs

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"

	"cod/utils"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func StartLogWatcher(ctx context.Context, clientset *kubernetes.Clientset, broadcast chan<- utils.Message) {
	namespace := os.Getenv("WATCH_NAMESPACE")
	if namespace == "" {
		log.Fatalf("WATCH_NAMESPACE environment variable not set")
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=couchbase-operator",
	})
	if err != nil {
		log.Printf("Error listing operator pods: %v", err)
		return
	}

	if len(pods.Items) == 0 {
		log.Printf("No operator pods found")
		return
	}

	podName := pods.Items[0].Name
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &v1.PodLogOptions{
		Container: "couchbase-operator",
		Follow:    true,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		log.Printf("Error getting log stream: %v", err)
		return
	}
	defer stream.Close()

	reader := bufio.NewReader(stream)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					log.Printf("Error reading log line: %v", err)
				}
				return
			}

			select {
			case <-ctx.Done():
				return
			case broadcast <- utils.Message{
				Type:    "log",
				Message: line,
			}:
			}
		}
	}
}
