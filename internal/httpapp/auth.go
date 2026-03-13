package httpapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type authContextKey string

const authUserIDContextKey authContextKey = "auth_user_id"
const authRoleContextKey authContextKey = "auth_role"

const (
	defaultAccessTTL  = 15 * time.Minute
	defaultRefreshTTL = 7 * 24 * time.Hour
)

var (
	jwtSecret        = []byte("dev-secret-change-in-prod")
	newTokenStringFn = generateSecureToken
	passwordHashCost = bcrypt.DefaultCost
	jwtSignStringFn  = func(token *jwt.Token, key []byte) (string, error) { return token.SignedString(key) }
	randReadFn       = rand.Read
)

type authAPI struct {
	mu sync.Mutex

	nextUserID    int64
	usersByID     map[int64]authUser
	usersByMail   map[string]authUser
	refreshTokens map[string]refreshTokenSession
}

type authUser struct {
	ID           int64
	Email        string
	Role         string
	PasswordHash string
	CreatedAt    time.Time
}

type refreshTokenSession struct {
	UserID    int64
	ExpiresAt time.Time
}

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type authTokensResponse struct {
	AccessToken           string `json:"access_token"`
	AccessTokenExpiresAt  string `json:"access_token_expires_at"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at"`
	TokenType             string `json:"token_type"`
}

type authMeResponse struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
}

func newAuthAPI() *authAPI {
	return &authAPI{
		nextUserID:    1,
		usersByID:     map[int64]authUser{},
		usersByMail:   map[string]authUser{},
		refreshTokens: map[string]refreshTokenSession{},
	}
}

func (a *authAPI) signup(w http.ResponseWriter, r *http.Request) {
	var in signupRequest
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "invalid_json", Message: "invalid JSON request body"})
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	in.Role = strings.TrimSpace(strings.ToLower(in.Role))
	if in.Role == "" {
		in.Role = "user"
	}
	if in.Role != "user" && in.Role != "admin" {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "request validation failed", Fields: map[string]string{"role": "role must be user or admin"}})
		return
	}
	if fields := validateAuthInput(in.Email, in.Password); len(fields) > 0 {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "request validation failed", Fields: fields})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), passwordHashCost)
	if err != nil {
		respondInternalServerError(w, r)
		return
	}

	a.mu.Lock()
	if _, exists := a.usersByMail[in.Email]; exists {
		a.mu.Unlock()
		respondErrorJSON(w, r, http.StatusConflict, apiError{Code: "email_exists", Message: "email already registered"})
		return
	}
	user := authUser{ID: a.nextUserID, Email: in.Email, Role: in.Role, PasswordHash: string(hash), CreatedAt: nowFn().UTC()}
	a.nextUserID++
	a.usersByID[user.ID] = user
	a.usersByMail[user.Email] = user
	a.mu.Unlock()

	tokens, err := a.issueTokens(user.ID, user.Role)
	if err != nil {
		respondInternalServerError(w, r)
		return
	}
	respondJSON(w, r, http.StatusCreated, map[string]any{"tokens": tokens})
}

func (a *authAPI) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "invalid_json", Message: "invalid JSON request body"})
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.Password == "" {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "request validation failed", Fields: map[string]string{"email": "email is required", "password": "password is required"}})
		return
	}

	a.mu.Lock()
	user, ok := a.usersByMail[in.Email]
	a.mu.Unlock()
	if !ok || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(in.Password)) != nil {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "invalid_credentials", Message: "invalid email or password"})
		return
	}

	tokens, err := a.issueTokens(user.ID, user.Role)
	if err != nil {
		respondInternalServerError(w, r)
		return
	}
	respondJSON(w, r, http.StatusOK, map[string]any{"tokens": tokens})
}

func (a *authAPI) refresh(w http.ResponseWriter, r *http.Request) {
	var in refreshRequest
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "invalid_json", Message: "invalid JSON request body"})
		return
	}
	in.RefreshToken = strings.TrimSpace(in.RefreshToken)
	if in.RefreshToken == "" {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "request validation failed", Fields: map[string]string{"refresh_token": "refresh_token is required"}})
		return
	}

	now := nowFn().UTC()
	a.mu.Lock()
	session, ok := a.refreshTokens[in.RefreshToken]
	if ok && session.ExpiresAt.Before(now) {
		delete(a.refreshTokens, in.RefreshToken)
		ok = false
	}
	if ok {
		delete(a.refreshTokens, in.RefreshToken)
	}
	_, userExists := a.usersByID[session.UserID]
	a.mu.Unlock()

	if !ok || !userExists {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "invalid_refresh_token", Message: "refresh token is invalid or expired"})
		return
	}

	tokens, err := a.issueTokens(session.UserID, a.userRoleByID(session.UserID))
	if err != nil {
		respondInternalServerError(w, r)
		return
	}
	respondJSON(w, r, http.StatusOK, map[string]any{"tokens": tokens})
}

func (a *authAPI) me(w http.ResponseWriter, r *http.Request) {
	uid, ok := authUserIDFromContext(r.Context())
	if !ok {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "missing authenticated user"})
		return
	}
	a.mu.Lock()
	user, exists := a.usersByID[uid]
	a.mu.Unlock()
	if !exists {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "user not found"})
		return
	}
	respondJSON(w, r, http.StatusOK, authMeResponse{UserID: user.ID, Email: user.Email})
}

func (a *authAPI) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := parseBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "missing or invalid authorization header"})
			return
		}
		uid, role, err := validateAccessToken(tok)
		if err != nil {
			respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "invalid or expired access token"})
			return
		}
		next.ServeHTTP(w, r.WithContext(withAuthPrincipal(r.Context(), uid, role)))
	})
}

func (a *authAPI) issueTokens(userID int64, role string) (authTokensResponse, error) {
	now := nowFn().UTC()
	accessExp := now.Add(defaultAccessTTL)
	refreshExp := now.Add(defaultRefreshTTL)

	claims := jwt.MapClaims{
		"sub":  userID,
		"exp":  accessExp.Unix(),
		"iat":  now.Unix(),
		"typ":  "access",
		"role": role,
	}
	accessToken, err := jwtSignStringFn(jwt.NewWithClaims(jwt.SigningMethodHS256, claims), jwtSecret)
	if err != nil {
		return authTokensResponse{}, err
	}

	refreshToken, err := newTokenStringFn()
	if err != nil {
		return authTokensResponse{}, err
	}

	a.mu.Lock()
	a.refreshTokens[refreshToken] = refreshTokenSession{UserID: userID, ExpiresAt: refreshExp}
	a.mu.Unlock()

	return authTokensResponse{
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExp.Format(time.RFC3339),
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: refreshExp.Format(time.RFC3339),
		TokenType:             "Bearer",
	}, nil
}

func validateAuthInput(email, password string) map[string]string {
	fields := map[string]string{}
	if email == "" || !strings.Contains(email, "@") {
		fields["email"] = "email must be valid"
	}
	if len(password) < 8 {
		fields["password"] = "password must be at least 8 characters"
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func parseBearerToken(header string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", errors.New("invalid authorization header")
	}
	return strings.TrimSpace(parts[1]), nil
}

func validateAccessToken(raw string) (int64, string, error) {
	tok, err := jwt.Parse(raw, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return jwtSecret, nil
	})
	if err != nil || !tok.Valid {
		return 0, "", errors.New("invalid token")
	}
	claims := tok.Claims.(jwt.MapClaims)
	if claims["typ"] != "access" {
		return 0, "", errors.New("invalid token type")
	}
	subVal, ok := claims["sub"]
	if !ok {
		return 0, "", errors.New("missing subject")
	}
	role, _ := claims["role"].(string)
	if role == "" {
		role = "user"
	}
	switch v := subVal.(type) {
	case float64:
		return int64(v), role, nil
	default:
		return 0, "", errors.New("invalid subject type")
	}
}

func withAuthUserID(ctx context.Context, uid int64) context.Context {
	return context.WithValue(ctx, authUserIDContextKey, uid)
}

func withAuthPrincipal(ctx context.Context, uid int64, role string) context.Context {
	return context.WithValue(withAuthUserID(ctx, uid), authRoleContextKey, role)
}

func authUserIDFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(authUserIDContextKey).(int64)
	return v, ok
}

func authRoleFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(authRoleContextKey).(string)
	return v, ok
}

func authPrincipalFromContext(ctx context.Context) (int64, string, bool) {
	uid, ok := authUserIDFromContext(ctx)
	if !ok {
		return 0, "", false
	}
	role, ok := authRoleFromContext(ctx)
	if !ok || role == "" {
		role = "user"
	}
	return uid, role, true
}

func (a *authAPI) userRoleByID(id int64) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.usersByID[id]
	if !ok {
		return "user"
	}
	if u.Role == "" {
		return "user"
	}
	return u.Role
}

func generateSecureToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := randReadFn(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
