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
		logger.Log.Fatal("WATCH_NAMESPACE environment variable not set - log watching disabled",
			zap.String("sessionId", logSessionId),
			zap.String("clusterName", clusterName))
		return
	}

	// Log the intent to start watching logs with useful context
	logContext := []zap.Field{
		zap.String("namespace", namespace),
		zap.String("sessionId", logSessionId),
		zap.String("clusterName", clusterName),
		zap.Bool("follow", follow),
	}
	if startTime != nil {
		logContext = append(logContext, zap.Time("startTime", *startTime))
	}
	if endTime != nil && !follow {
		logContext = append(logContext, zap.Time("endTime", *endTime))
	}
	logger.Log.Info("Starting log watcher", logContext...)

	// List operator pods
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=couchbase-operator",
	})
	if err != nil {
		logger.Log.Error("Failed to list operator pods",
			zap.Error(err),
			zap.String("namespace", namespace),
			zap.String("sessionId", logSessionId))
		return
	}

	if len(pods.Items) == 0 {
		logger.Log.Warn("No operator pods found - log watching disabled",
			zap.String("namespace", namespace),
			zap.String("sessionId", logSessionId),
			zap.String("labelSelector", "app=couchbase-operator"))
		return
	}

	podName := pods.Items[0].Name

	// Configure log options
	logOptions := &v1.PodLogOptions{
		Container: "couchbase-operator",
		Follow:    follow,
	}

	if startTime != nil {
		sinceTime := metav1.NewTime(*startTime)
		logOptions.SinceTime = &sinceTime
	}

	// Stream logs
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, logOptions)
	stream, err := req.Stream(ctx)
	if err != nil {
		logger.Log.Error("Failed to establish log stream",
			zap.Error(err),
			zap.String("namespace", namespace),
			zap.String("podName", podName),
			zap.String("sessionId", logSessionId))
		return
	}
	defer stream.Close()

	logger.Log.Info("Log stream established",
		zap.String("podName", podName),
		zap.String("namespace", namespace),
		zap.String("sessionId", logSessionId))

	// Process log stream
	reader := bufio.NewReader(stream)
	lineCount := 0
	for {
		select {
		case <-ctx.Done():
			logger.Log.Info("Log watcher terminated",
				zap.String("sessionId", logSessionId),
				zap.Int("linesProcessed", lineCount))
			return
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					logger.Log.Error("Failed reading log line",
						zap.Error(err),
						zap.String("podName", podName),
						zap.String("sessionId", logSessionId))
				} else {
					logger.Log.Info("Log stream ended (EOF)",
						zap.String("sessionId", logSessionId),
						zap.Int("linesProcessed", lineCount))
				}
				return
			}

			// Parse the log entry
			var logEntry LogEntry
			if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
				// Only log parsing errors at debug level to avoid log spam
				logger.Log.Debug("Failed parsing log entry (sending anyway)",
					zap.Error(err),
					zap.String("sessionId", logSessionId))
			} else {
				// Check end time if specified and not following
				if endTime != nil && !follow && logEntry.Time.After(*endTime) {
					logger.Log.Info("Log watcher reached end time",
						zap.String("sessionId", logSessionId),
						zap.Int("linesProcessed", lineCount))
					return
				}

				// If clusterName is specified, only send logs for that cluster
				if clusterName != "" && (logEntry.Cluster == "" || logEntry.Cluster != namespace+"/"+clusterName) {
					continue
				}
			}

			// Send log line to client
			select {
			case <-ctx.Done():
				return
			case broadcast <- utils.Message{
				Type:      "log",
				SessionID: logSessionId,
				Message:   line,
			}:
				lineCount++
			}
		}
	}
}
