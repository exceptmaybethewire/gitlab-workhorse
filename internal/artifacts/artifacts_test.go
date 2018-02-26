package artifacts

import (
	"os"
	"testing"

	log "github.com/sirupsen/logrus"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

func TestMain(m *testing.M) {
	cleanup, err := testhelper.BuildExecutables()
	if err != nil {
		log.WithError(err).Print("Test setup: failed to build executables")
		os.Exit(1)
	}

	os.Exit(func() int {
		defer cleanup()
		return m.Run()
	}())

}
