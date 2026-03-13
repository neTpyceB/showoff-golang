# Auth Service (JWT + Refresh Tokens)

HTTP auth module with signup/login, JWT access tokens, refresh-token rotation, and middleware-protected route.

## Location

- Auth implementation: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/auth.go`
- Tests: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/httpapp/auth_test.go`

## Endpoints

- `POST /auth/signup`
- `POST /auth/login`
- `POST /auth/refresh`
- `GET /auth/me` (requires `Authorization: Bearer <access_token>`)

## Flow

1. Signup/login returns:
   - `access_token` (JWT)
   - `refresh_token`
   - expiry timestamps
2. Protected routes validate access token via middleware.
3. Refresh endpoint rotates refresh token (one-time use).

## Example

```bash
# signup
curl -X POST http://localhost:8080/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"email":"user@example.com","password":"password123"}'

# login
curl -X POST http://localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"user@example.com","password":"password123"}'

# refresh
curl -X POST http://localhost:8080/auth/refresh \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<refresh-token>"}'

# protected route
curl http://localhost:8080/auth/me \
  -H 'Authorization: Bearer <access-token>'
```

## Notes

- Access token algorithm: HS256
- Password hashing: bcrypt
- Current storage is in-memory (users + refresh tokens)
