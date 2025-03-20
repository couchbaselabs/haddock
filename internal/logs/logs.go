package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"cod/internal/logger"
	"cod/internal/utils"

	"go.uber.org/zap"
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
		logger.Log.Fatal("WATCH_NAMESPACE environment variable not set")
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=couchbase-operator",
	})
	if err != nil {
		logger.Log.Error("Error listing operator pods", zap.Error(err))
		return
	}

	if len(pods.Items) == 0 {
		logger.Log.Warn("No operator pods found")
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
		logger.Log.Error("Error getting log stream", zap.Error(err))
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
					logger.Log.Error("Error reading log line", zap.Error(err))
				}
				return
			}

			// Parse just the timestamp
			var logTime LogTimestamp
			if err := json.Unmarshal([]byte(line), &logTime); err != nil {
				logger.Log.Error("Error parsing log timestamp", zap.Error(err))
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
