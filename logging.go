package main

import (
	goLog "log"
	"os"
	"os/signal"
	"syscall"

	"github.com/client9/reopen"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

func reopenLogWriter(l reopen.WriteCloser, sighup chan os.Signal) {
	for _ = range sighup {
		log.Print("Reopening log file")
		l.Reopen()
	}
}

func prepareLoggingFile(logFile string) *reopen.FileWriter {
	file, err := reopen.NewFileWriter(logFile)
	if err != nil {
		goLog.Fatalf("Unable to set output log: %s", err)
	}

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	go reopenLogWriter(file, sighup)
	return file
}

const (
	jsonLogFormat = "json"
	textLogFormat = "text"
	noneLogType   = "none"
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
