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

// LogEntry extracts timestamp and cluster from the log entry
type LogEntry struct {
	Time    time.Time `json:"ts"`
	Cluster string    `json:"cluster,omitempty"`
}

func StartLogWatcher(ctx context.Context, clientset *kubernetes.Clientset, broadcast chan<- utils.Message, startTime, endTime *time.Time, follow bool, clusterName string, logSessionId string) {
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

			// Parse the log entry
			var logEntry LogEntry
			if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
				logger.Log.Error("Error parsing log entry", zap.Error(err))
				continue
			}

			// Check end time if specified and not following
			if endTime != nil && !follow {
				if logEntry.Time.After(*endTime) {
					return
				}
			}

			// If clusterName is specified, only send logs for that cluster
			if clusterName != "" {
				// Only send if the log entry is for the specified cluster, check if namespace + clusterName matches logEntry.Cluster
				if logEntry.Cluster == "" || logEntry.Cluster != namespace+"/"+clusterName {
					continue
				}
			}

			select {
			case <-ctx.Done():
				return
			case broadcast <- utils.Message{
				Type:      "log",
				SessionID: logSessionId,
				Message:   line,
			}:
			}
		}
	}
}
