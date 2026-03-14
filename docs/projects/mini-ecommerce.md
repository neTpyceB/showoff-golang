# Mini E-commerce Backend

## Endpoints

- `POST /products` (auth required, `admin` role)
- `GET /products`
- `POST /orders` (auth required, requires `Idempotency-Key` header)
- `GET /orders/{id}` (auth required, owner or admin)

## Data Model

- `products`
- `orders`
- `order_items`
- `payment_transactions`
- `idempotency_keys`

Migration: `internal/httpapp/migrations/004_ecommerce.sql`

## Behavior

- Order creation runs in one DB transaction.
- Product rows are locked (`FOR UPDATE`) before stock checks.
- Stock is reduced only for paid orders.
- Payment provider is mocked (`pm_fail` forces payment failure).
- Repeated `Idempotency-Key` for same user returns the original order response.

## Example (Docker-first)

Create admin:

```bash
ADMIN_TOKEN=$(curl -s -X POST http://localhost:8080/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"StrongPass123!","role":"admin"}' | jq -r '.data.tokens.access_token')
```

Create product:

```bash
curl -X POST http://localhost:8080/products \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Mechanical Keyboard","price_cents":12999,"stock_qty":10}'
```

Create order:

```bash
USER_TOKEN=$(curl -s -X POST http://localhost:8080/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"email":"user@example.com","password":"StrongPass123!"}' | jq -r '.data.tokens.access_token')

curl -X POST http://localhost:8080/orders \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: order-001' \
  -d '{"items":[{"product_id":1,"quantity":1}],"payment_method":"pm_ok"}'
```
