package logger

import (
	"context"
	"os"

	"github.com/sirupsen/logrus"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
)

// Logger wraps logrus.Logger with service-specific context
type Logger struct {
	*logrus.Logger
	service string
}

// New creates a new logger for the given service
func New(serviceName, logLevel string) *Logger {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
	})
	log.SetOutput(os.Stdout)

	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	log.SetLevel(level)

	return &Logger{
		Logger:  log,
		service: serviceName,
	}
}

// WithContext returns a log entry with context fields
func (l *Logger) WithContext(ctx context.Context) *logrus.Entry {
	entry := l.Logger.WithField("service", l.service)

	// Extract request ID from context if available
	if requestID, ok := ctxpkg.GetRequestID(ctx); ok {
		entry = entry.WithField("request_id", requestID)
	}

	// Extract job ID from context if available
	if jobID, ok := ctxpkg.GetJobID(ctx); ok {
		entry = entry.WithField("job_id", jobID)
	}

	// Extract tenant ID from context if available
	if tenantID, ok := ctxpkg.GetTenantID(ctx); ok {
		entry = entry.WithField("tenant_id", tenantID)
	}

	return entry
}

// WithRequestID returns a log entry with request ID
func (l *Logger) WithRequestID(requestID string) *logrus.Entry {
	return l.Logger.WithFields(logrus.Fields{
		"service":    l.service,
		"request_id": requestID,
	})
}

// WithJobID returns a log entry with job ID
func (l *Logger) WithJobID(jobID string) *logrus.Entry {
	return l.Logger.WithFields(logrus.Fields{
		"service": l.service,
		"job_id":  jobID,
	})
}

// WithTenantID returns a log entry with tenant ID
func (l *Logger) WithTenantID(tenantID string) *logrus.Entry {
	return l.Logger.WithFields(logrus.Fields{
		"service":   l.service,
		"tenant_id": tenantID,
	})
}

// WithFields returns a log entry with additional fields
// Creates a copy of the fields map to avoid mutating the caller's map
func (l *Logger) WithFields(fields logrus.Fields) *logrus.Entry {
	// Create a copy of the fields map to avoid mutating the caller's map
	copiedFields := make(logrus.Fields, len(fields)+1)
	for k, v := range fields {
		copiedFields[k] = v
	}
	copiedFields["service"] = l.service
	return l.Logger.WithFields(copiedFields)
}
