package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"

	"github.com/client9/reopen"
)

func reopenLogWriter(l reopen.WriteCloser, sighup chan os.Signal) {
	for range sighup {
		log.Printf("Reopening log file")
		l.Reopen()
	}
}

func startLogging(logFile string) {
	var logWriter = reopen.Stderr

	if logFile != "" {
		file, err := reopen.NewFileWriter(logFile)
		if err != nil {
			log.Fatalf("Unable to set output log: %s", err)
		}
		logWriter = file
	}

	log.SetOutput(logWriter)
	helper.SetCustomResponseLogger(logWriter)

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	go reopenLogWriter(logWriter, sighup)
}
