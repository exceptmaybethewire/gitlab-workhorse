package upload

import (
	"fmt"
	"mime/multipart"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/secret"

	"github.com/dgrijalva/jwt-go"
)

const RewrittenFieldsHeader = "Gitlab-Workhorse-Multipart-Fields"

type savedFileTracker struct {
	request         *http.Request
	rewrittenFields map[string]string
}

type MultipartClaims struct {
	RewrittenFields map[string]string `json:"rewritten_fields"`
	jwt.StandardClaims
}

func Accelerate(tempDir string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := &savedFileTracker{request: r}
		HandleFileUploads(w, r, h, tempDir, s)
	})
}

func (s *savedFileTracker) ProcessFile(fieldName, fileName string, _ *multipart.Writer) error {
	if s.rewrittenFields == nil {
		s.rewrittenFields = make(map[string]string)
	}
	s.rewrittenFields[fieldName] = fileName
	return nil
}

func (_ *savedFileTracker) ProcessField(_ string, _ *multipart.Writer) error {
	return nil
}

func (s *savedFileTracker) Finalize() error {
	if s.rewrittenFields == nil {
		return nil
	}

	claims := MultipartClaims{s.rewrittenFields, secret.DefaultClaims}
	tokenString, err := secret.JWTTokenString(claims)
	if err != nil {
		return fmt.Errorf("savedFileTracker.Finalize: %v", err)
	}

	s.request.Header.Set(RewrittenFieldsHeader, tokenString)
	return nil
}

func (a *savedFileTracker) Name() string {
	return "accelerate"
}
