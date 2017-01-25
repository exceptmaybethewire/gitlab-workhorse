package main

import (
	goLog "log"
	"os"
	"os/signal"
	"syscall"

	log "github.com/Sirupsen/logrus"
	logstash "github.com/bshuster-repo/logrus-logstash-hook"
	"github.com/client9/reopen"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

func reopenLogWriter(l reopen.WriteCloser, sighup chan os.Signal) {
	for _ = range sighup {
		log.Printf("Reopening log file")
		l.Reopen()
	}
}

func prepareLoggingFile(logFile string) *reopen.FileWriter {
	file, err := reopen.NewFileWriter(logFile)
	if err != nil {
		log.Fatalf("Unable to set output log: %s", err)
	}

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	go reopenLogWriter(file, sighup)
	return file
}

const (
	jsonLogFormat   = "json"
	textLogFormat   = "text"
	noneLogType     = "none"
	logstashLogType = "logstash"
)

type logConfiguration struct {
	accessLogEnabled bool
	accessLogFile    string
	logFile          string
	logOutput        string
	logFormat        string
}

func startLogging(config logConfiguration) {
	if config.accessLogEnabled {
		var accessLogWriter = reopen.Stderr

		if config.accessLogFile != "" {
			accessLogWriter = prepareLoggingFile(config.accessLogFile)

		}
		helper.SetAccessLogWriter(accessLogWriter)
	}

	switch config.logFormat {
	case noneLogType:
		log.SetOutput(reopen.Discard)
		return
	case jsonLogFormat:
		log.SetFormatter(&log.JSONFormatter{})
	case textLogFormat:
		log.SetFormatter(&log.TextFormatter{})
	case logstashLogType:
		log.SetFormatter(&logstash.LogstashFormatter{})
	default:
		log.WithField("logFormat", config.logFormat).Error("Unknown logFormat configured")
	}

	if config.logFile != "" {
		file := prepareLoggingFile(config.logFile)
		goLog.SetOutput(file)
		log.SetOutput(file)

	} else {
		log.SetOutput(reopen.Stderr)
	}
}
