package logger

import (
	"context"

	"github.com/sirupsen/logrus"
)

// Logger defines the logging interface
type Log interface {
	// Core logging methods
	Info(args ...interface{})
	Debug(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	Fatal(args ...interface{})

	// Formatted logging methods
	Infof(format string, args ...interface{})
	Debugf(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})

	// Field-based logging methods
	WithContext(ctx context.Context) *logrus.Entry
	WithRequestID(requestID string) *logrus.Entry
	WithJobID(jobID string) *logrus.Entry
	WithTenantID(tenantID string) *logrus.Entry
	WithFields(fields logrus.Fields) *logrus.Entry
	WithField(key string, value interface{}) *logrus.Entry
	WithError(err error) *logrus.Entry
}

// Ensure Logger implements Log interface
var _ Log = (*Logger)(nil)
