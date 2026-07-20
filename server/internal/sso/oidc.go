package sso

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCClient wraps the OIDC provider + OAuth2 config for Keycloak.
type OIDCClient struct {
	provider   *oidc.Provider
	oauth2     *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	httpClient *http.Client // non-nil when skipTLSVerify; used for token exchange + userinfo
}

// NewOIDCClient discovers Keycloak endpoints via {issuer}/.well-known/openid-configuration.
// The issuer URL is the full Keycloak realm URL, e.g.
// "https://larasati.lintasarta.co.id/realms/dev".
//
// If skipTLSVerify is true, the HTTP client used for OIDC discovery and token
// exchange will skip certificate verification. This is intended for internal
// CA environments (e.g. self-hosted Keycloak with a private CA) where the
// system trust store does not contain the CA. Use only when you trust the
// network path to the issuer.
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, redirectURL string, skipTLSVerify bool) (*OIDCClient, error) {
	httpClient := &http.Client{}
	if skipTLSVerify {
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
	}
	// go-oidc uses the HTTP client from the context for discovery + JWKS fetch.
	discoveryCtx := oidc.ClientContext(ctx, httpClient)

	provider, err := oidc.NewProvider(discoveryCtx, issuer)
	if err != nil {
		return nil, fmt.Errorf("sso: oidc discovery failed for %q: %w", issuer, err)
	}

	return &OIDCClient{
		provider: provider,
		oauth2: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier:   provider.Verifier(&oidc.Config{ClientID: clientID}),
		httpClient: httpClient,
	}, nil
}

// AuthURL builds the Keycloak authorization URL with PKCE (S256) + state.
// The caller stores codeVerifier + state in a short-lived cookie and verifies
// them on the callback.
func (c *OIDCClient) AuthURL(state, codeVerifier string) string {
	return c.oauth2.AuthCodeURL(state,
		oauth2.S256ChallengeOption(codeVerifier),
	)
}

// ExchangeCode exchanges the authorization code for tokens, verifies the ID
// token signature via the OIDC discovery JWKS, and returns the email claim.
// Falls back to the userinfo endpoint if the email claim is absent from the
// ID token (some Keycloak configs omit it by default).
//
// If the client was created with skipTLSVerify, the same insecure HTTP client
// is used for token exchange and userinfo fetch.
func (c *OIDCClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (string, error) {
	if c.httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	}
	token, err := c.oauth2.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return "", fmt.Errorf("sso: token exchange failed: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", errors.New("sso: id_token missing from token response")
	}

	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", fmt.Errorf("sso: id token verification failed: %w", err)
	}

	var claims struct {
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", fmt.Errorf("sso: parse id token claims: %w", err)
	}

	if claims.Email != "" {
		return claims.Email, nil
	}

	// Fallback: fetch email from the userinfo endpoint.
	userInfoCtx := ctx
	if c.httpClient != nil {
		userInfoCtx = oidc.ClientContext(ctx, c.httpClient)
	}
	userInfo, err := c.provider.UserInfo(userInfoCtx, oauth2.StaticTokenSource(token))
	if err != nil {
		return "", fmt.Errorf("sso: userinfo fetch failed: %w", err)
	}
	if err := userInfo.Claims(&claims); err != nil {
		return "", fmt.Errorf("sso: parse userinfo claims: %w", err)
	}
	if claims.Email == "" {
		return "", errors.New("sso: email claim not found in id token or userinfo")
	}
	return claims.Email, nil
}

// GenerateCodeVerifier returns a PKCE code_verifier (43-128 chars, base64url).
// See RFC 7636 §4.1.
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sso: generate code verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateState returns a random state parameter for CSRF protection.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sso: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LogoutURL returns the Keycloak end_session_endpoint URL with
// post_logout_redirect_uri and client_id params. The end_session_endpoint
// is discovered from the OIDC well-known configuration.
//
// postLogoutRedirectURI must be registered as a "Valid Post Logout Redirect URI"
// in the Keycloak client configuration, otherwise Keycloak will ignore it
// and redirect to its default logout page.
func (c *OIDCClient) LogoutURL(postLogoutRedirectURI string) (string, error) {
	var claims struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := c.provider.Claims(&claims); err != nil {
		return "", fmt.Errorf("sso: parse discovery claims for end_session_endpoint: %w", err)
	}
	if claims.EndSessionEndpoint == "" {
		return "", errors.New("sso: end_session_endpoint not found in discovery document")
	}

	u, err := url.Parse(claims.EndSessionEndpoint)
	if err != nil {
		return "", fmt.Errorf("sso: parse end_session_endpoint: %w", err)
	}

	q := u.Query()
	if postLogoutRedirectURI != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}
	// client_id helps Keycloak identify the client session to end and
	// skip the "Are you sure?" confirmation page.
	q.Set("client_id", c.oauth2.ClientID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}