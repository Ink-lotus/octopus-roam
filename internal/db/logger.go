package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/privacy"
	"gorm.io/gorm/logger"
)

// Debug SQL retains statement structure, timing and row counts. Bound values
// and driver errors may contain credentials, including values from imports
// that have no request redactor, so they must not enter the debug output.
type redactedLogger struct{ logger.Interface }

func (l redactedLogger) LogMode(level logger.LogLevel) logger.Interface {
	return redactedLogger{l.Interface.LogMode(level)}
}

func (l redactedLogger) ParamsFilter(_ context.Context, sql string, _ ...any) (string, []any) {
	return sql, nil
}

func (l redactedLogger) Trace(ctx context.Context, begin time.Time, query func() (string, int64), err error) {
	if err != nil && !errors.Is(err, logger.ErrRecordNotFound) {
		err = errors.New(privacy.Mask(err.Error()))
	}
	l.Interface.Trace(ctx, begin, query, err)
}

func (l redactedLogger) Error(ctx context.Context, message string, data ...any) {
	safe := make([]any, len(data))
	for i, value := range data {
		safe[i] = privacy.Mask(fmt.Sprint(value))
	}
	l.Interface.Error(ctx, message, safe...)
}
