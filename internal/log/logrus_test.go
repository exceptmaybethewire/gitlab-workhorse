package log_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/log"
)

func requireCorrelationID(t *testing.T, getEntry func(ctx context.Context) *logrus.Entry) *logrus.Entry {
	id := "test-id"
	ctx := context.WithValue(context.Background(), log.KeyCorrelationID, id)

	e := getEntry(ctx)

	require.NotNil(t, e)
	require.Contains(t, e.Data, "correlation-id")
	require.Equal(t, id, e.Data["correlation-id"])

	return e
}

func TestWithContext(t *testing.T) {
	e := requireCorrelationID(t, log.WithContext)
	require.Len(t, e.Data, 1)
}

func TestWithFields(t *testing.T) {
	fields := log.Fields{
		"one": "ok",
		"two": 2,
	}

	toTest := func(ctx context.Context) *logrus.Entry {
		return log.WithFields(ctx, fields)
	}

	e := requireCorrelationID(t, toTest)

	for key, value := range fields {
		require.Contains(t, e.Data, key)
		require.Equal(t, value, e.Data[key])
	}
	require.Len(t, e.Data, len(fields)+1)
}

func TestWithField(t *testing.T) {
	key := "key"
	value := struct{ Name string }{"Test"}
	toTest := func(ctx context.Context) *logrus.Entry {
		return log.WithField(ctx, key, value)
	}

	e := requireCorrelationID(t, toTest)

	require.Contains(t, e.Data, key)
	require.Equal(t, value, e.Data[key])
	require.Len(t, e.Data, 2)
}

func TestWithError(t *testing.T) {
	err := errors.New("An error")
	toTest := func(ctx context.Context) *logrus.Entry {
		return log.WithError(ctx, err)
	}

	e := requireCorrelationID(t, toTest)

	require.Contains(t, e.Data, logrus.ErrorKey)
	require.Equal(t, err, e.Data[logrus.ErrorKey])
	require.Len(t, e.Data, 2)
}

func TestNoContext(t *testing.T) {
	key := "key"
	value := struct{ Name string }{"Test"}

	logger := log.NoContext()
	require.Equal(t, logrus.StandardLogger(), logger)

	e := logger.WithField(key, value)
	require.NotContains(t, e.Data, "correlation-id")

	require.Contains(t, e.Data, key)
	require.Equal(t, value, e.Data[key])
	require.Len(t, e.Data, 1)
}
