package artifacts

import (
	"os"
	"testing"

	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/testhelper"
)

func TestMain(m *testing.M) {
	if err := testhelper.BuildExecutables(); err != nil {
		log.WithError(err).Fatal()
	}

	os.Exit(m.Run())

}
