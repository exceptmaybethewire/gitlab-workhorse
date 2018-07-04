package upload

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/http"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/secret"

	jwt "github.com/dgrijalva/jwt-go"
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

type PreAuthorizer interface {
	PreAuthorizeHandler(next api.HandleFunc, suffix string) http.Handler
}

func Accelerate(rails PreAuthorizer, h http.Handler) http.Handler {
	return rails.PreAuthorizeHandler(func(w http.ResponseWriter, r *http.Request, a *api.Response) {
		s := &savedFileTracker{request: r}
		HandleFileUploads(w, r, h, a, s)
	}, "/authorize")
}

func (s *savedFileTracker) ProcessFile(_ context.Context, fieldName string, file *filestore.FileHandler, _ *multipart.Writer) error {
	if s.rewrittenFields == nil {
		s.rewrittenFields = make(map[string]string)
	}
	s.rewrittenFields[fieldName] = file.LocalPath
	return nil
}

func (_ *savedFileTracker) ProcessField(_ context.Context, _ string, _ *multipart.Writer) error {
	return nil
}

func (s *savedFileTracker) Finalize(_ context.Context) error {
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
