package webauth

import (
	"crypto/sha1"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/provider"
	"github.com/go-pkgz/auth/v2/token"
	"github.com/golang-jwt/jwt/v5"
	"github.com/parMaster/zoomrs/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuthService_OnlyManagersPass(t *testing.T) {
	svc, err := NewAuthService(config.Server{
		Domain:    "example.com",
		JWTSecret: "jwt-secret",
		Managers:  []string{"boss@example.com"},
	})
	require.NoError(t, err)

	m := svc.Middleware()
	protected := m.Auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func(email string) int {
		tkn, err := svc.TokenService().Token(token.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				Issuer:    "zoom-record-service",
				// auth v2 requires exactly one audience; Google logins carry an empty one
				Audience: jwt.ClaimStrings{""},
			},
			User: &token.User{ID: "google_x", Name: "x", Email: email},
		})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-JWT", tkn)
		rw := httptest.NewRecorder()
		protected.ServeHTTP(rw, req)
		return rw.Code
	}

	assert.Equal(t, http.StatusOK, call("boss@example.com"))
	assert.Equal(t, http.StatusUnauthorized, call("stranger@example.com"))
}

func TestMapGoogleUser(t *testing.T) {
	u := mapGoogleUser(provider.UserData{
		"id": "1234", "name": "John", "email": "j@example.com", "picture": "https://example.com/p.jpg",
	}, nil)
	assert.Equal(t, "j@example.com", u.Email)
	assert.Equal(t, "John", u.Name)
	assert.Equal(t, "https://example.com/p.jpg", u.Picture)
	assert.Equal(t, "google_"+token.HashID(sha1.New(), "1234"), u.ID)
}

func TestNewAuthService_RegistersGoogle(t *testing.T) {
	svc, err := NewAuthService(config.Server{Domain: "example.com", JWTSecret: "s"})
	require.NoError(t, err)

	p, err := svc.Provider("google")
	require.NoError(t, err)
	assert.Equal(t, "google", p.Name())
}
