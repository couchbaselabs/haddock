package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"time"

	"cod/utils"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LogTimestamp only extracts the timestamp from the log entry
type LogTimestamp struct {
	Time time.Time `json:"ts"`
}

func StartLogWatcher(ctx context.Context, clientset *kubernetes.Clientset, broadcast chan<- utils.Message, startTime, endTime *time.Time, follow bool) {
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

	logOptions := &v1.PodLogOptions{
		Container: "couchbase-operator",
		Follow:    follow,
	}

	if startTime != nil {
		sinceTime := metav1.NewTime(*startTime)
		logOptions.SinceTime = &sinceTime
	}

	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, logOptions)
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

			// Parse just the timestamp
			var logTime LogTimestamp
			if err := json.Unmarshal([]byte(line), &logTime); err != nil {
				log.Printf("Error parsing log timestamp: %v", err)
				continue
			}

			// Check end time if specified and not following
			if endTime != nil && !follow {
				if logTime.Time.After(*endTime) {
					return
				}
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
