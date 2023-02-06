package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"

	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/metrics"
	"github.com/grafana/grafana/pkg/login"
	"github.com/grafana/grafana/pkg/login/social"
	"github.com/grafana/grafana/pkg/middleware/cookies"
	"github.com/grafana/grafana/pkg/services/authn"
	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	loginservice "github.com/grafana/grafana/pkg/services/login"
	"github.com/grafana/grafana/pkg/services/org"
	"github.com/grafana/grafana/pkg/services/user"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/util/errutil"
	"github.com/grafana/grafana/pkg/web"
)

var (
	oauthLogger = log.New("oauth")
)

const (
	OauthStateCookieName = "oauth_state"
	OauthPKCECookieName  = "oauth_code_verifier"
)

func GenStateString() (string, error) {
	rnd := make([]byte, 32)
	if _, err := rand.Read(rnd); err != nil {
		oauthLogger.Error("failed to generate state string", "err", err)
		return "", err
	}
	return base64.URLEncoding.EncodeToString(rnd), nil
}

// genPKCECode returns a random URL-friendly string and it's base64 URL encoded SHA256 digest.
func genPKCECode() (string, string, error) {
	// IETF RFC 7636 specifies that the code verifier should be 43-128
	// characters from a set of unreserved URI characters which is
	// almost the same as the set of characters in base64url.
	// https://datatracker.ietf.org/doc/html/rfc7636#section-4.1
	//
	// It doesn't hurt to generate a few more bytes here, we generate
	// 96 bytes which we then encode using base64url to make sure
	// they're within the set of unreserved characters.
	//
	// 96 is chosen because 96*8/6 = 128, which means that we'll have
	// 128 characters after it has been base64 encoded.
	raw := make([]byte, 96)
	_, err := rand.Read(raw)
	if err != nil {
		return "", "", err
	}
	ascii := make([]byte, 128)
	base64.RawURLEncoding.Encode(ascii, raw)

	shasum := sha256.Sum256(ascii)
	pkce := base64.RawURLEncoding.EncodeToString(shasum[:])
	return string(ascii), pkce, nil
}

func (hs *HTTPServer) OAuthLogin(ctx *contextmodel.ReqContext) {
	name := web.Params(ctx.Req)[":name"]
	loginInfo := loginservice.LoginInfo{AuthModule: name}

	if errorParam := ctx.Query("error"); errorParam != "" {
		errorDesc := ctx.Query("error_description")
		oauthLogger.Error("failed to login ", "error", errorParam, "errorDesc", errorDesc)
		hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, login.ErrProviderDeniedRequest, "error", errorParam, "errorDesc", errorDesc)
		return
	}

	code := ctx.Query("code")

	if hs.Features.IsEnabled(featuremgmt.FlagAuthnService) {
		req := &authn.Request{HTTPRequest: ctx.Req, Resp: ctx.Resp}
		if code == "" {
			redirect, err := hs.authnService.RedirectURL(ctx.Req.Context(), authn.ClientWithPrefix(name), req)
			if err != nil {
				hs.handleAuthnOAuthErr(ctx, "failed to generate oauth redirect url", err)
				return
			}

			if pkce := redirect.Extra[authn.KeyOAuthPKCE]; pkce != "" {
				cookies.WriteCookie(ctx.Resp, OauthPKCECookieName, pkce, hs.Cfg.OAuthCookieMaxAge, hs.CookieOptionsFromCfg)
			}

			cookies.WriteCookie(ctx.Resp, OauthStateCookieName, redirect.Extra[authn.KeyOAuthState], hs.Cfg.OAuthCookieMaxAge, hs.CookieOptionsFromCfg)
			ctx.Redirect(redirect.URL)
			return
		}

		identity, err := hs.authnService.Login(ctx.Req.Context(), authn.ClientWithPrefix(name), req)
		// NOTE: always delete these cookies, even if login failed
		cookies.DeleteCookie(ctx.Resp, OauthPKCECookieName, hs.CookieOptionsFromCfg)
		cookies.DeleteCookie(ctx.Resp, OauthStateCookieName, hs.CookieOptionsFromCfg)

		if err != nil {
			hs.handleAuthnOAuthErr(ctx, "failed to perform login for oauth request", err)
			return
		}

		metrics.MApiLoginOAuth.Inc()
		cookies.WriteSessionCookie(ctx, hs.Cfg, identity.SessionToken.UnhashedToken, hs.Cfg.LoginMaxLifetime)

		redirectURL := setting.AppSubUrl + "/"
		if redirectTo := ctx.GetCookie("redirect_to"); len(redirectTo) > 0 && hs.ValidateRedirectTo(redirectTo) == nil {
			redirectURL = redirectTo
			cookies.DeleteCookie(ctx.Resp, "redirect_to", hs.CookieOptionsFromCfg)
		}

		ctx.Redirect(redirectURL)
		return
	}

	provider := hs.SocialService.GetOAuthInfoProvider(name)
	if provider == nil {
		hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, errors.New("OAuth not enabled"))
		return
	}

	connect, err := hs.SocialService.GetConnector(name)
	if err != nil {
		hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, fmt.Errorf("no OAuth with name %s configured", name))
		return
	}

	if code == "" {
		var opts []oauth2.AuthCodeOption
		if provider.UsePKCE {
			ascii, pkce, err := genPKCECode()
			if err != nil {
				ctx.Logger.Error("Generating PKCE failed", "error", err)
				hs.handleOAuthLoginError(ctx, loginInfo, LoginError{
					HttpStatus:    http.StatusInternalServerError,
					PublicMessage: "An internal error occurred",
				})
				return
			}

			cookies.WriteCookie(ctx.Resp, OauthPKCECookieName, ascii, hs.Cfg.OAuthCookieMaxAge, hs.CookieOptionsFromCfg)

			opts = append(opts,
				oauth2.SetAuthURLParam("code_challenge", pkce),
				oauth2.SetAuthURLParam("code_challenge_method", "S256"),
			)
		}

		state, err := GenStateString()
		if err != nil {
			ctx.Logger.Error("Generating state string failed", "err", err)
			hs.handleOAuthLoginError(ctx, loginInfo, LoginError{
				HttpStatus:    http.StatusInternalServerError,
				PublicMessage: "An internal error occurred",
			})
			return
		}

		hashedState := hs.hashStatecode(state, provider.ClientSecret)
		cookies.WriteCookie(ctx.Resp, OauthStateCookieName, hashedState, hs.Cfg.OAuthCookieMaxAge, hs.CookieOptionsFromCfg)
		if provider.HostedDomain != "" {
			opts = append(opts, oauth2.SetAuthURLParam("hd", provider.HostedDomain))
		}

		ctx.Redirect(connect.AuthCodeURL(state, opts...))
		return
	}

	cookieState := ctx.GetCookie(OauthStateCookieName)

	// delete cookie
	cookies.DeleteCookie(ctx.Resp, OauthStateCookieName, hs.CookieOptionsFromCfg)

	if cookieState == "" {
		hs.handleOAuthLoginError(ctx, loginInfo, LoginError{
			HttpStatus:    http.StatusInternalServerError,
			PublicMessage: "login.OAuthLogin(missing saved state)",
		})
		return
	}

	queryState := hs.hashStatecode(ctx.Query("state"), provider.ClientSecret)
	oauthLogger.Info("state check", "queryState", queryState, "cookieState", cookieState)
	if cookieState != queryState {
		hs.handleOAuthLoginError(ctx, loginInfo, LoginError{
			HttpStatus:    http.StatusInternalServerError,
			PublicMessage: "login.OAuthLogin(state mismatch)",
		})
		return
	}

	oauthClient, err := hs.SocialService.GetOAuthHttpClient(name)
	if err != nil {
		ctx.Logger.Error("Failed to create OAuth http client", "error", err)
		hs.handleOAuthLoginError(ctx, loginInfo, LoginError{
			HttpStatus:    http.StatusInternalServerError,
			PublicMessage: "login.OAuthLogin(" + err.Error() + ")",
		})
		return
	}

	oauthCtx := context.WithValue(context.Background(), oauth2.HTTPClient, oauthClient)
	opts := []oauth2.AuthCodeOption{}

	codeVerifier := ctx.GetCookie(OauthPKCECookieName)
	cookies.DeleteCookie(ctx.Resp, OauthPKCECookieName, hs.CookieOptionsFromCfg)
	if codeVerifier != "" {
		opts = append(opts,
			oauth2.SetAuthURLParam("code_verifier", codeVerifier),
		)
	}

	// get token from provider
	token, err := connect.Exchange(oauthCtx, code, opts...)
	if err != nil {
		hs.handleOAuthLoginError(ctx, loginInfo, LoginError{
			HttpStatus:    http.StatusInternalServerError,
			PublicMessage: "login.OAuthLogin(NewTransportWithCode)",
			Err:           err,
		})
		return
	}
	// token.TokenType was defaulting to "bearer", which is out of spec, so we explicitly set to "Bearer"
	token.TokenType = "Bearer"

	if hs.Cfg.Env != setting.Dev {
		oauthLogger.Debug("OAuthLogin: got token",
			"expiry", fmt.Sprintf("%v", token.Expiry),
			"type", token.TokenType,
			"has_refresh_token", token.RefreshToken != "",
		)
	} else {
		oauthLogger.Debug("OAuthLogin: got token",
			"expiry", fmt.Sprintf("%v", token.Expiry),
			"type", token.TokenType,
			"access_token", fmt.Sprintf("%v", token.AccessToken),
			"refresh_token", fmt.Sprintf("%v", token.RefreshToken),
		)
	}

	// set up oauth2 client
	client := connect.Client(oauthCtx, token)

	// get user info
	userInfo, err := connect.UserInfo(client, token)
	if err != nil {
		var sErr *social.Error
		if errors.As(err, &sErr) {
			hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, sErr)
		} else {
			hs.handleOAuthLoginError(ctx, loginInfo, LoginError{
				HttpStatus:    http.StatusInternalServerError,
				PublicMessage: fmt.Sprintf("login.OAuthLogin(get info from %s)", name),
				Err:           err,
			})
		}
		return
	}

	oauthLogger.Debug("OAuthLogin got user info", "userInfo", fmt.Sprintf("%v", userInfo))

	// validate that we got at least an email address
	if userInfo.Email == "" {
		hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, login.ErrNoEmail)
		return
	}

	// validate that the email is allowed to login to grafana
	if !connect.IsEmailAllowed(userInfo.Email) {
		hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, login.ErrEmailNotAllowed)
		return
	}

	loginInfo.ExternalUser = *hs.buildExternalUserInfo(token, userInfo, name)
	loginInfo.User, err = hs.SyncUser(ctx, &loginInfo.ExternalUser, connect)
	if err != nil {
		hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, err)
		return
	}

	// login
	if err := hs.loginUserWithUser(loginInfo.User, ctx); err != nil {
		hs.handleOAuthLoginErrorWithRedirect(ctx, loginInfo, err)
		return
	}

	loginInfo.HTTPStatus = http.StatusOK
	hs.HooksService.RunLoginHook(&loginInfo, ctx)
	metrics.MApiLoginOAuth.Inc()

	if redirectTo := ctx.GetCookie("redirect_to"); len(redirectTo) > 0 {
		if err := hs.ValidateRedirectTo(redirectTo); err == nil {
			cookies.DeleteCookie(ctx.Resp, "redirect_to", hs.CookieOptionsFromCfg)
			ctx.Redirect(redirectTo)
			return
		}
		ctx.Logger.Debug("Ignored invalid redirect_to cookie value", "redirect_to", redirectTo)
	}

	ctx.Redirect(setting.AppSubUrl + "/")
}

// buildExternalUserInfo returns a ExternalUserInfo struct from OAuth user profile
func (hs *HTTPServer) buildExternalUserInfo(token *oauth2.Token, userInfo *social.BasicUserInfo, name string) *loginservice.ExternalUserInfo {
	oauthLogger.Debug("Building external user info from OAuth user info")

	extUser := &loginservice.ExternalUserInfo{
		AuthModule:     fmt.Sprintf("oauth_%s", name),
		OAuthToken:     token,
		AuthId:         userInfo.Id,
		Name:           userInfo.Name,
		Login:          userInfo.Login,
		Email:          userInfo.Email,
		OrgRoles:       map[int64]org.RoleType{},
		Groups:         userInfo.Groups,
		IsGrafanaAdmin: userInfo.IsGrafanaAdmin,
	}

	// FIXME: this is a hack to support the old way of assigning roles to users
	// we should remove this once we have a better way of assigning roles to users
	// potentially add externallySynced logic here
	if userInfo.Role != "" && !hs.Cfg.OAuthSkipOrgRoleUpdateSync {
		rt := userInfo.Role
		if rt.IsValid() {
			// The user will be assigned a role in either the auto-assigned organization or in the default one
			var orgID int64
			if hs.Cfg.AutoAssignOrg && hs.Cfg.AutoAssignOrgId > 0 {
				orgID = int64(hs.Cfg.AutoAssignOrgId)
				plog.Debug("The user has a role assignment and organization membership is auto-assigned",
					"role", userInfo.Role, "orgId", orgID)
			} else {
				orgID = int64(1)
				plog.Debug("The user has a role assignment and organization membership is not auto-assigned",
					"role", userInfo.Role, "orgId", orgID)
			}
			extUser.OrgRoles[orgID] = rt
		}
	}

	return extUser
}

// SyncUser syncs a Grafana user profile with the corresponding OAuth profile.
func (hs *HTTPServer) SyncUser(
	ctx *contextmodel.ReqContext,
	extUser *loginservice.ExternalUserInfo,
	connect social.SocialConnector,
) (*user.User, error) {
	oauthLogger.Debug("Syncing Grafana user with corresponding OAuth profile")
	// add/update user in Grafana
	cmd := &loginservice.UpsertUserCommand{
		ReqContext:    ctx,
		ExternalUser:  extUser,
		SignupAllowed: connect.IsSignupAllowed(),
		UserLookupParams: loginservice.UserLookupParams{
			Email:  &extUser.Email,
			UserID: nil,
			Login:  nil,
		},
	}

	if err := hs.Login.UpsertUser(ctx.Req.Context(), cmd); err != nil {
		return nil, err
	}

	// Do not expose disabled status,
	// just show incorrect user credentials error (see #17947)
	if cmd.Result.IsDisabled {
		oauthLogger.Warn("User is disabled", "user", cmd.Result.Login)
		return nil, login.ErrInvalidCredentials
	}

	return cmd.Result, nil
}

func (hs *HTTPServer) hashStatecode(code, seed string) string {
	hashBytes := sha256.Sum256([]byte(code + hs.Cfg.SecretKey + seed))
	return hex.EncodeToString(hashBytes[:])
}

func (hs *HTTPServer) handleAuthnOAuthErr(c *contextmodel.ReqContext, msg string, err error) {
	gfErr := &errutil.Error{}
	if errors.As(err, gfErr) {
		if gfErr.Public().Message != "" {
			c.Handle(hs.Cfg, gfErr.Public().StatusCode, gfErr.Public().Message, err)
			return
		}
	}

	c.Logger.Warn(msg, "err", err)
	c.Redirect(hs.Cfg.AppSubURL + "/login")
}

type LoginError struct {
	HttpStatus    int
	PublicMessage string
	Err           error
}

func (hs *HTTPServer) handleOAuthLoginError(ctx *contextmodel.ReqContext, info loginservice.LoginInfo, err LoginError) {
	ctx.Handle(hs.Cfg, err.HttpStatus, err.PublicMessage, err.Err)

	// login hooks is handled by authn.Service
	if !hs.Features.IsEnabled(featuremgmt.FlagAuthnService) {
		info.Error = err.Err
		if info.Error == nil {
			info.Error = errors.New(err.PublicMessage)
		}
		info.HTTPStatus = err.HttpStatus

		hs.HooksService.RunLoginHook(&info, ctx)
	}
}

func (hs *HTTPServer) handleOAuthLoginErrorWithRedirect(ctx *contextmodel.ReqContext, info loginservice.LoginInfo, err error, v ...interface{}) {
	hs.redirectWithError(ctx, err, v...)

	// login hooks is handled by authn.Service
	if !hs.Features.IsEnabled(featuremgmt.FlagAuthnService) {
		info.Error = err
		hs.HooksService.RunLoginHook(&info, ctx)
	}
}
