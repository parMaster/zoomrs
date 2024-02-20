package webauth

import (
	"crypto/sha1"
	"slices"
	"time"

	"github.com/go-pkgz/auth"
	"github.com/go-pkgz/auth/avatar"
	"github.com/go-pkgz/auth/provider"
	"github.com/go-pkgz/auth/token"
	"github.com/go-pkgz/lgr"
	"github.com/parMaster/zoomrs/config"
	"golang.org/x/oauth2"
)

func NewAuthService(cfg config.Server) (*auth.Service, error) {
	options := auth.Opts{
		SecretReader: token.SecretFunc(func(id string) (string, error) { // secret key for JWT
			return cfg.JWTSecret, nil
		}),
		TokenDuration:     time.Minute * 5, // token expires in 5 minutes
		CookieDuration:    time.Hour * 24,  // cookie expires in 1 day and will enforce re-login
		Issuer:            "zoom-record-service",
		URL:               "https://" + cfg.Domain,
		AvatarStore:       avatar.NewLocalFS("/tmp/zoomrs"),
		AvatarResizeLimit: 200,
		Validator: token.ValidatorFunc(func(_ string, claims token.Claims) bool {
			// allow access to managers
			return slices.Contains(cfg.Managers, claims.User.Email)
		}),
		ClaimsUpd: token.ClaimsUpdFunc(func(claims token.Claims) token.Claims { // modify issued token
			return claims
		}),
		Logger:      lgr.Std,
		DisableXSRF: cfg.OAuthDisableXSRF,
	}

	// create auth authService with providers
	authService := auth.NewService(options)

	c := auth.Client{
		Cid:     cfg.OAuthClientId,
		Csecret: cfg.OAuthClientSecret,
	}

	authService.AddCustomProvider("google", c, provider.CustomHandlerOpt{
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
		InfoURL: "https://www.googleapis.com/oauth2/v2/userinfo",
		MapUserFn: func(data provider.UserData, _ []byte) token.User {
			userInfo := token.User{
				ID:    "google_" + token.HashID(sha1.New(), data.Value("username")),
				Name:  data.Value("nickname"),
				Email: data.Value("email"),
			}
			return userInfo
		},
		Scopes: []string{"email"},
	})
	return authService, nil
}
