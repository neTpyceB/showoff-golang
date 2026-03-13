package httpapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthSignupLoginRefreshAndMeFlow(t *testing.T) {
	restoreAuthGlobals(t)
	nowFn = time.Now
	newTokenStringFn = func() (string, error) { return "refresh-token-1", nil }

	h := NewHandler()

	rec := doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":"u@example.com","password":"password123"}`)
	assertStatus(t, rec, http.StatusCreated)
	var signup struct {
		Data struct {
			Tokens authTokensResponse `json:"tokens"`
		} `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &signup)
	if signup.Data.Tokens.AccessToken == "" || signup.Data.Tokens.RefreshToken != "refresh-token-1" {
		t.Fatalf("tokens = %+v", signup.Data.Tokens)
	}

	rec = doRequest(t, h, http.MethodGet, "/auth/me", "")
	assertStatus(t, rec, http.StatusUnauthorized)

	rec = doRequestWithHeaders(t, h, http.MethodGet, "/auth/me", "", map[string]string{"Authorization": "Bearer " + signup.Data.Tokens.AccessToken})
	assertStatus(t, rec, http.StatusOK)
	var me struct {
		Data authMeResponse `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &me)
	if me.Data.UserID != 1 || me.Data.Email != "u@example.com" {
		t.Fatalf("me = %+v", me.Data)
	}

	newTokenStringFn = func() (string, error) { return "refresh-token-2", nil }
	rec = doRequest(t, h, http.MethodPost, "/auth/login", `{"email":"u@example.com","password":"password123"}`)
	assertStatus(t, rec, http.StatusOK)
	var login struct {
		Data struct {
			Tokens authTokensResponse `json:"tokens"`
		} `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &login)
	if login.Data.Tokens.RefreshToken != "refresh-token-2" {
		t.Fatalf("login refresh = %+v", login.Data.Tokens)
	}

	newTokenStringFn = func() (string, error) { return "refresh-token-3", nil }
	rec = doRequest(t, h, http.MethodPost, "/auth/refresh", `{"refresh_token":"refresh-token-2"}`)
	assertStatus(t, rec, http.StatusOK)
	var refresh struct {
		Data struct {
			Tokens authTokensResponse `json:"tokens"`
		} `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &refresh)
	if refresh.Data.Tokens.RefreshToken != "refresh-token-3" {
		t.Fatalf("refresh tokens = %+v", refresh.Data.Tokens)
	}

	rec = doRequest(t, h, http.MethodPost, "/auth/refresh", `{"refresh_token":"refresh-token-2"}`)
	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestAuthValidationAndErrorBranches(t *testing.T) {
	restoreAuthGlobals(t)
	newTokenStringFn = func() (string, error) { return "r1", nil }
	h := NewHandler()

	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":"bad","password":"short"}`), http.StatusBadRequest)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":"u@example.com","password":"password123"}`), http.StatusCreated)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":"u@example.com","password":"password123"}`), http.StatusConflict)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/login", `{"email":"u@example.com","password":"bad"}`), http.StatusUnauthorized)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/login", `{"email":"","password":""}`), http.StatusBadRequest)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":`), http.StatusBadRequest)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/refresh", `{"refresh_token":""}`), http.StatusBadRequest)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/refresh", `{"refresh_token":`), http.StatusBadRequest)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/refresh", `{"refresh_token":"missing"}`), http.StatusUnauthorized)
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/login", `{"email":`), http.StatusBadRequest)
}

func TestAuthHelpers(t *testing.T) {
	restoreAuthGlobals(t)
	a := newAuthAPI()

	if fields := validateAuthInput("", "x"); fields["email"] == "" || fields["password"] == "" {
		t.Fatalf("fields = %#v", fields)
	}
	if fields := validateAuthInput("u@example.com", "password123"); fields != nil {
		t.Fatalf("fields = %#v", fields)
	}

	if _, err := parseBearerToken(""); err == nil {
		t.Fatal("expected parse bearer error")
	}
	if tok, err := parseBearerToken("Bearer abc"); err != nil || tok != "abc" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": float64(7), "exp": time.Now().Add(time.Hour).Unix(), "typ": "access"})
	signed, _ := tok.SignedString(jwtSecret)
	uid, err := validateAccessToken(signed)
	if err != nil || uid != 7 {
		t.Fatalf("uid=%d err=%v", uid, err)
	}

	badMethod := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{"sub": float64(7), "exp": time.Now().Add(time.Hour).Unix(), "typ": "access"})
	badSigned, _ := badMethod.SignedString(jwtSecret)
	if _, err := validateAccessToken(badSigned); err == nil {
		t.Fatal("expected invalid method error")
	}

	wrongType := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": float64(7), "exp": time.Now().Add(time.Hour).Unix(), "typ": "refresh"})
	wrongTypeSigned, _ := wrongType.SignedString(jwtSecret)
	if _, err := validateAccessToken(wrongTypeSigned); err == nil {
		t.Fatal("expected invalid type")
	}

	noSub := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix(), "typ": "access"})
	noSubSigned, _ := noSub.SignedString(jwtSecret)
	if _, err := validateAccessToken(noSubSigned); err == nil {
		t.Fatal("expected missing sub")
	}

	strSub := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "x", "exp": time.Now().Add(time.Hour).Unix(), "typ": "access"})
	strSubSigned, _ := strSub.SignedString(jwtSecret)
	if _, err := validateAccessToken(strSubSigned); err == nil {
		t.Fatal("expected invalid sub type")
	}

	ctx := withAuthUserID(context.Background(), 9)
	if uid, ok := authUserIDFromContext(ctx); !ok || uid != 9 {
		t.Fatalf("uid=%d ok=%v", uid, ok)
	}
	if _, ok := authUserIDFromContext(context.Background()); ok {
		t.Fatal("expected no auth user id")
	}

	tokStr, err := generateSecureToken()
	if err != nil || len(tokStr) == 0 {
		t.Fatalf("token=%q err=%v", tokStr, err)
	}

	newTokenStringFn = func() (string, error) { return "", errors.New("token error") }
	if _, err := a.issueTokens(1); err == nil {
		t.Fatal("expected token generation error")
	}
	newTokenStringFn = func() (string, error) { return "r-ok", nil }
	jwtSignStringFn = func(*jwt.Token, []byte) (string, error) { return "", errors.New("sign error") }
	if _, err := a.issueTokens(1); err == nil {
		t.Fatal("expected sign error")
	}

	randReadFn = func([]byte) (int, error) { return 0, errors.New("rand failed") }
	if _, err := generateSecureToken(); err == nil {
		t.Fatal("expected rand error")
	}

	hash := strings.Repeat("x", 10)
	a.mu.Lock()
	a.usersByID[1] = authUser{ID: 1, Email: "x@x.com", PasswordHash: hash}
	a.usersByMail["x@x.com"] = a.usersByID[1]
	a.mu.Unlock()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	a.me(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/auth/me", nil).WithContext(withAuthUserID(context.Background(), 99))
	a.me(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestAuthRefreshExpiredToken(t *testing.T) {
	restoreAuthGlobals(t)
	a := newAuthAPI()
	a.mu.Lock()
	a.usersByID[1] = authUser{ID: 1, Email: "u@example.com"}
	a.refreshTokens["r-expired"] = refreshTokenSession{UserID: 1, ExpiresAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	a.mu.Unlock()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(`{"refresh_token":"r-expired"}`))
	a.refresh(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestAuthMiddlewareAndIssueTokensErrorBranches(t *testing.T) {
	restoreAuthGlobals(t)
	h := NewHandler()
	assertStatus(t, doRequestWithHeaders(t, h, http.MethodGet, "/auth/me", "", map[string]string{"Authorization": "Bearer bad.token"}), http.StatusUnauthorized)

	passwordHashCost = 100
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":"x@example.com","password":"password123"}`), http.StatusInternalServerError)

	passwordHashCost = 4
	newTokenStringFn = func() (string, error) { return "", errors.New("boom") }
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":"y@example.com","password":"password123"}`), http.StatusInternalServerError)

	newTokenStringFn = func() (string, error) { return "r-ok", nil }
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/signup", `{"email":"z@example.com","password":"password123"}`), http.StatusCreated)
	newTokenStringFn = func() (string, error) { return "", errors.New("boom") }
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/login", `{"email":"z@example.com","password":"password123"}`), http.StatusInternalServerError)

	newTokenStringFn = func() (string, error) { return "r-login", nil }
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/login", `{"email":"z@example.com","password":"password123"}`), http.StatusOK)
	newTokenStringFn = func() (string, error) { return "", errors.New("boom") }
	assertStatus(t, doRequest(t, h, http.MethodPost, "/auth/refresh", `{"refresh_token":"r-login"}`), http.StatusInternalServerError)
}

func restoreAuthGlobals(t *testing.T) {
	t.Helper()
	oldNow := nowFn
	oldSecret := jwtSecret
	oldNewToken := newTokenStringFn
	oldCost := passwordHashCost
	oldSign := jwtSignStringFn
	oldRandRead := randReadFn
	t.Cleanup(func() {
		nowFn = oldNow
		jwtSecret = oldSecret
		newTokenStringFn = oldNewToken
		passwordHashCost = oldCost
		jwtSignStringFn = oldSign
		randReadFn = oldRandRead
	})
}

func doRequestWithHeaders(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
